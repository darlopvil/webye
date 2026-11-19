package main

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every request to CeX goes through a guardian. This is the only place in
// webye that creates an HTTP client: search, product details and images all
// call obtener, so nothing can reach CeX without the cache, the concurrency
// cap and the circuit breaker below.

const (
	// While measuring how long CeX really takes when it is slow (#21): the
	// most webye can wait and still send the page, 5 s under the server's
	// 30 s write timeout. CeX's service worker gives up on the network
	// after 3 s only to fall back to its cache; with nothing cached the
	// browser keeps waiting, so 3 s was sooner than the website itself.
	tiempoMaximo = 25 * time.Second

	// No more parallel requests per upstream than a browser opens per host
	// over HTTP/1.1.
	maxSimultaneas = 6

	// The heaviest response CeX's own website asks for is the full search
	// (539 KB, with every facet). Nothing webye asks for can legitimately
	// be bigger than twice that.
	tamanoMaximo = 1 << 20

	// Half of the Go runtime soft limit (GOMEMLIMIT=32MiB in the example
	// compose file), leaving the other half for requests in flight and
	// templates.
	cacheMaxima = 16 << 20

	// Without a Retry-After header, the first pause lasts one minute and
	// doubles with every block in a row, up to one hour. A successful
	// response resets it.
	pausaInicial = time.Minute
	pausaMaxima  = time.Hour

	agenteUsuario = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"

	// Cache lifetimes, taken from the ones CeX allows itself in its own
	// service worker (es.webuy.com/sw.js). Search results: one hour, like its
	// wss2 search listings (/v3/boxes?q=…); prices change in a nightly batch.
	// Product details: five minutes, like /v3/boxes/{id}/detail, which CeX
	// labels "high-churn stock data". Images pass 0 and follow their own
	// Cache-Control max-age.
	ttlBusqueda = time.Hour
	ttlFicha    = 5 * time.Minute
)

// One shared RAM cache and one guardian per upstream.
var (
	cacheCeX = nuevaCache(cacheMaxima)
	algolia  = nuevoGuardian("algolia", "application/json", cacheCeX)
	wss2     = nuevoGuardian("wss2", "application/json", cacheCeX)
	imagenes = conRedirecciones(nuevoGuardian("imagenes", "image/", cacheCeX))
)

var (
	errPausado      = errors.New("upstream paused after a block")
	errDemasiado    = errors.New("upstream response too large")
	errRedireccion  = errors.New("unexpected upstream redirect")
	errContenido    = errors.New("unexpected upstream content type")
	errBloqueo      = errors.New("upstream blocked the request")
	reTituloHTML    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reMaxAge        = regexp.MustCompile(`(?i)(?:^|[,\s])max-age=(\d+)`)
	reSinAlmacenaje = regexp.MustCompile(`(?i)(?:^|[,\s])(no-store|no-cache|private)(?:$|[,\s])`)
)

// errorRespuesta carries the status that made the guardian give up.
type errorRespuesta struct {
	Estado int
	causa  error
}

func (e *errorRespuesta) Error() string { return fmt.Sprintf("%v: status %d", e.causa, e.Estado) }
func (e *errorRespuesta) Unwrap() error { return e.causa }

// respuesta is what the callers get back: only the parts webye uses.
type respuesta struct {
	Estado int
	Tipo   string
	Cuerpo []byte
	// Caduca is when webye's cached copy expires; zero when it is not kept.
	Caduca time.Time
}

// guardian protects one upstream (Algolia, the wss2 API or the image host).
type guardian struct {
	nombre       string
	tipoEsperado string // "application/json" or "image/"
	cliente      *http.Client
	cache        *cache
	huecos       chan struct{}
	vuelo        grupoVuelo
	ahora        func() time.Time

	// admiteRedireccion hands redirects to the caller, and caches them,
	// instead of failing: CeX's image host answers a missing photo with a
	// redirect to a generic picture.
	admiteRedireccion bool

	mu         sync.Mutex
	pausaHasta time.Time
	pausa      time.Duration

	// Result of the last request that really went upstream, for /salud.
	ultimaVez    time.Time
	ultimoEstado int
	ultimoFallo  string

	tiempos tiempos
}

// tiempos counts how long upstream answers take, since start-up, to pick
// the timeout from real numbers (#21). Only durations: no URLs.
type tiempos struct {
	peticiones int
	mas1       int // took 1 s or more
	mas3       int
	mas10      int
	agotadas   int // gave up after tiempoMaximo
	masLenta   time.Duration
}

func (t tiempos) texto() string {
	segundos := strings.Replace(strconv.FormatFloat(t.masLenta.Seconds(), 'f', 1, 64), ".", ",", 1)
	peticiones := "peticiones"
	if t.peticiones == 1 {
		peticiones = "petición"
	}
	return fmt.Sprintf("%d %s; tardaron 1 s o más: %d, 3 s o más: %d, 10 s o más: %d; agotadas: %d; la más lenta: %s s",
		t.peticiones, peticiones, t.mas1, t.mas3, t.mas10, t.agotadas, segundos)
}

func nuevoCliente() *http.Client {
	return &http.Client{
		Timeout: tiempoMaximo,
		// CeX's APIs and images never redirect. A redirect would mean
		// something changed upstream, so it is returned as is, never followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:               nil,
			ForceAttemptHTTP2:   true,
			MaxIdleConnsPerHost: maxSimultaneas,
			IdleConnTimeout:     90 * time.Second,
		},
		// No cookie jar: nothing CeX sets is ever sent back.
		Jar: nil,
	}
}

func nuevoGuardian(nombre, tipoEsperado string, c *cache) *guardian {
	return &guardian{
		nombre:       nombre,
		tipoEsperado: tipoEsperado,
		cliente:      nuevoCliente(),
		cache:        c,
		huecos:       make(chan struct{}, maxSimultaneas),
		ahora:        time.Now,
	}
}

// conRedirecciones makes a guardian hand redirects to its caller.
func conRedirecciones(g *guardian) *guardian {
	g.admiteRedireccion = true
	return g
}

// obtener returns the response for clave from the cache or, if it is not
// there, builds the request with nueva and sends it. ttl is how long a 200
// response is kept; 0 means "as long as the upstream max-age says".
func (g *guardian) obtener(ctx context.Context, clave string, ttl time.Duration, nueva func(context.Context) (*http.Request, error)) (respuesta, error) {
	clave = g.nombre + "|" + clave
	if r, ok := g.cache.leer(clave, g.ahora()); ok {
		return r, nil
	}
	if hasta, pausado := g.pausado(); pausado {
		return respuesta{}, fmt.Errorf("%w until %s", errPausado, hasta.Format(time.RFC3339))
	}
	return g.vuelo.hacer(clave, func() (respuesta, error) {
		// One cancelled visitor must not break the request other visitors
		// are waiting for; tiempoMaximo still bounds it.
		ctx := context.WithoutCancel(ctx)

		g.huecos <- struct{}{}
		defer func() { <-g.huecos }()

		// Another request may have filled the cache while this one waited.
		if r, ok := g.cache.leer(clave, g.ahora()); ok {
			return r, nil
		}
		if hasta, pausado := g.pausado(); pausado {
			return respuesta{}, fmt.Errorf("%w until %s", errPausado, hasta.Format(time.RFC3339))
		}

		req, err := nueva(ctx)
		if err != nil {
			return respuesta{}, err
		}
		req.Header.Set("User-Agent", agenteUsuario)

		inicio := time.Now()
		r, err := g.llamar(req, clave, ttl)
		g.anotar(r.Estado, err, time.Since(inicio))
		return r, err
	})
}

// llamar sends one request upstream and checks the answer.
func (g *guardian) llamar(req *http.Request, clave string, ttl time.Duration) (respuesta, error) {
	res, err := g.cliente.Do(req)
	if err != nil {
		// Go's error repeats the full URL, which can carry what the
		// visitor was looking at. Keep only the host.
		var ue *url.Error
		if errors.As(err, &ue) {
			return respuesta{}, fmt.Errorf("%s %s: %w", ue.Op, req.URL.Host, ue.Err)
		}
		return respuesta{}, err
	}
	defer res.Body.Close()

	cuerpo, err := io.ReadAll(io.LimitReader(res.Body, tamanoMaximo+1))
	if err != nil {
		return respuesta{}, err
	}
	if len(cuerpo) > tamanoMaximo {
		return respuesta{}, errDemasiado
	}

	r := respuesta{Estado: res.StatusCode, Tipo: res.Header.Get("Content-Type"), Cuerpo: cuerpo}
	if err := g.evaluar(res, cuerpo); err != nil {
		return respuesta{}, err
	}
	redireccion := r.Estado >= 300 && r.Estado < 400
	if r.Estado == http.StatusOK || (g.admiteRedireccion && redireccion) {
		if ttl == 0 {
			ttl = maxAge(res.Header)
		}
		if ttl > 0 {
			r.Caduca = g.ahora().Add(ttl)
			g.cache.guardar(clave, r, r.Caduca)
		}
	}
	return r, nil
}

// anotar keeps the result of the last real request for /salud.
func (g *guardian) anotar(estado int, err error, duracion time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ultimaVez = g.ahora()
	g.ultimoEstado = estado
	g.ultimoFallo = ""
	if err != nil {
		g.ultimoFallo = err.Error()
	}

	t := &g.tiempos
	t.peticiones++
	t.masLenta = max(t.masLenta, duracion)
	for _, c := range []struct {
		limite time.Duration
		cuenta *int
	}{{time.Second, &t.mas1}, {3 * time.Second, &t.mas3}, {10 * time.Second, &t.mas10}} {
		if duracion >= c.limite {
			*c.cuenta++
		}
	}
	var red net.Error
	agotada := errors.As(err, &red) && red.Timeout()
	if agotada {
		t.agotadas++
	}
	if duracion >= time.Second || agotada {
		log.Printf("upstream %s slow: %.1fs, status %d, timed out %v", g.nombre, duracion.Seconds(), estado, agotada)
	}
}

// fallo records a problem the caller found in an answer the guardian
// accepted, such as JSON that no longer has the expected shape.
func (g *guardian) fallo(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ultimoFallo = err.Error()
}

// evaluar decides whether a response means CeX is pushing back. Blocks open
// the circuit breaker; anything else is handed to the caller.
func (g *guardian) evaluar(res *http.Response, cuerpo []byte) error {
	estado := res.StatusCode
	switch {
	case estado == http.StatusTooManyRequests, estado == http.StatusForbidden, estado >= 500:
		g.pausar(res, cuerpo)
		return &errorRespuesta{Estado: estado, causa: errBloqueo}
	case estado >= 300 && estado < 400 && !g.admiteRedireccion:
		return fmt.Errorf("%w: status %d", errRedireccion, estado)
	case estado == http.StatusOK && !g.tipoValido(res.Header.Get("Content-Type")):
		// A challenge page served with a 200 instead of the expected JSON
		// or image is a block too.
		g.pausar(res, cuerpo)
		return fmt.Errorf("%w: %q", errContenido, res.Header.Get("Content-Type"))
	}
	if estado == http.StatusOK {
		g.mu.Lock()
		g.pausa = 0
		g.mu.Unlock()
	}
	return nil
}

func (g *guardian) tipoValido(tipo string) bool {
	medio, _, err := mime.ParseMediaType(tipo)
	if err != nil {
		return false
	}
	if strings.HasSuffix(g.tipoEsperado, "/") {
		return strings.HasPrefix(medio, g.tipoEsperado)
	}
	return medio == g.tipoEsperado
}

func (g *guardian) pausado() (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pausaHasta, g.ahora().Before(g.pausaHasta)
}

func (g *guardian) pausar(res *http.Response, cuerpo []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()

	duracion, motivo := retryAfter(res.Header.Get("Retry-After"), g.ahora())
	if duracion <= 0 {
		switch {
		case g.pausa == 0:
			g.pausa = pausaInicial
		case g.pausa < pausaMaxima:
			g.pausa = min(g.pausa*2, pausaMaxima)
		}
		duracion, motivo = g.pausa, "backoff"
	}
	g.pausaHasta = g.ahora().Add(duracion)

	// Everything needed to understand the block, and nothing that could
	// identify a visitor: no URL (it can carry what was being looked at),
	// no body (block pages usually print the client IP), only its title.
	titulo := ""
	if m := reTituloHTML.FindSubmatch(cuerpo); m != nil {
		titulo = strings.TrimSpace(string(m[1]))
	}
	log.Printf("upstream %s paused for %s (%s): status %d, content-type %q, server %q, cf-ray %q, retry-after %q, body %d B, title %q",
		g.nombre, duracion, motivo, res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Server"),
		res.Header.Get("Cf-Ray"), res.Header.Get("Retry-After"), len(cuerpo), titulo)
}

// retryAfter reads a Retry-After header in either of its two forms.
func retryAfter(valor string, ahora time.Time) (time.Duration, string) {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return 0, ""
	}
	if s, err := strconv.Atoi(valor); err == nil && s > 0 {
		return time.Duration(s) * time.Second, "retry-after"
	}
	if t, err := http.ParseTime(valor); err == nil && t.After(ahora) {
		return t.Sub(ahora), "retry-after"
	}
	return 0, ""
}

// maxAge returns how long the upstream allows a response to be reused, or 0
// if it forbids it or does not say.
func maxAge(h http.Header) time.Duration {
	valor := strings.Join(h.Values("Cache-Control"), ", ")
	if reSinAlmacenaje.MatchString(valor) {
		return 0
	}
	if m := reMaxAge.FindStringSubmatch(valor); m != nil {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			return time.Duration(s) * time.Second
		}
	}
	return 0
}

// ---------- in-memory cache ----------

// cache keeps responses in RAM only, never on disk, within a byte budget.
// When the budget is exceeded, the oldest entries go first.
type cache struct {
	mu      sync.Mutex
	limite  int
	tamano  int
	orden   *list.List
	entrada map[string]*list.Element
}

type elementoCache struct {
	clave  string
	r      respuesta
	expira time.Time
}

func nuevaCache(limite int) *cache {
	return &cache{limite: limite, orden: list.New(), entrada: map[string]*list.Element{}}
}

func (c *cache) leer(clave string, ahora time.Time) (respuesta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entrada[clave]
	if !ok {
		return respuesta{}, false
	}
	el := e.Value.(*elementoCache)
	if !ahora.Before(el.expira) {
		c.quitar(e)
		return respuesta{}, false
	}
	return el.r, true
}

func (c *cache) guardar(clave string, r respuesta, expira time.Time) {
	tam := len(clave) + len(r.Cuerpo) + len(r.Tipo)
	if tam > c.limite {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entrada[clave]; ok {
		c.quitar(e)
	}
	c.entrada[clave] = c.orden.PushBack(&elementoCache{clave: clave, r: r, expira: expira})
	c.tamano += tam
	for c.tamano > c.limite {
		c.quitar(c.orden.Front())
	}
}

func (c *cache) quitar(e *list.Element) {
	el := e.Value.(*elementoCache)
	c.orden.Remove(e)
	delete(c.entrada, el.clave)
	c.tamano -= len(el.clave) + len(el.r.Cuerpo) + len(el.r.Tipo)
}

// ---------- in-flight deduplication ----------

// grupoVuelo collapses identical requests in flight into one: if ten
// visitors open the same page at once, CeX gets a single request.
type grupoVuelo struct {
	mu sync.Mutex
	m  map[string]*llamada
}

type llamada struct {
	hecho chan struct{}
	r     respuesta
	err   error
}

func (g *grupoVuelo) hacer(clave string, fn func() (respuesta, error)) (respuesta, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]*llamada{}
	}
	if l, ok := g.m[clave]; ok {
		g.mu.Unlock()
		<-l.hecho
		return l.r, l.err
	}
	l := &llamada{hecho: make(chan struct{})}
	g.m[clave] = l
	g.mu.Unlock()

	l.r, l.err = fn()
	close(l.hecho)

	g.mu.Lock()
	delete(g.m, clave)
	g.mu.Unlock()
	return l.r, l.err
}
