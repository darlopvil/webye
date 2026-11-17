package main

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every visit that misses the cache costs a request to CeX, so the routes
// that can reach CeX are rate limited per client. A visitor over the limit
// gets a 429 and nothing leaves the server.

const (
	// The same budget SimpleEbay gives its pages, already proven with real
	// browsing: 30 pages in a row, refilled at 30 per minute.
	rafagaPaginas = 30
	ritmoPaginas  = 30.0 / 60 // pages per second

	// Most clients tracked at once (about 1 MB of buckets). When it is full
	// and idle buckets cannot be freed, new clients are refused rather than
	// let through untracked.
	maxClientes = 10000
)

// limitePaginas guards search and product pages. Image routes get their own
// limiter once a results page has a fixed number of images.
var limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)

type limitador struct {
	mu      sync.Mutex
	rafaga  float64
	ritmo   float64
	cubos   map[string]*cubo
	ahora   func() time.Time
	limpiar time.Time
}

type cubo struct {
	fichas float64
	visto  time.Time
}

func nuevoLimitador(rafaga int, ritmo float64) *limitador {
	return &limitador{
		rafaga: float64(rafaga),
		ritmo:  ritmo,
		cubos:  map[string]*cubo{},
		ahora:  time.Now,
	}
}

// permitir takes one token from the client's bucket. When there is none, it
// says how long until the next one.
func (l *limitador) permitir(cliente string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ahora := l.ahora()

	// A bucket idle for longer than a full refill is full again, so it can
	// be forgotten without changing anyone's limit.
	lleno := time.Duration(l.rafaga / l.ritmo * float64(time.Second))
	if ahora.Sub(l.limpiar) >= lleno || len(l.cubos) >= maxClientes {
		for k, c := range l.cubos {
			if ahora.Sub(c.visto) >= lleno {
				delete(l.cubos, k)
			}
		}
		l.limpiar = ahora
	}

	c, ok := l.cubos[cliente]
	if !ok {
		if len(l.cubos) >= maxClientes {
			return false, time.Duration(float64(time.Second) / l.ritmo)
		}
		c = &cubo{fichas: l.rafaga, visto: ahora}
		l.cubos[cliente] = c
	}
	c.fichas = math.Min(l.rafaga, c.fichas+ahora.Sub(c.visto).Seconds()*l.ritmo)
	c.visto = ahora
	if c.fichas < 1 {
		return false, time.Duration((1 - c.fichas) / l.ritmo * float64(time.Second))
	}
	c.fichas--
	return true, 0
}

// envolver applies the limiter to a handler.
func (l *limitador) envolver(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, espera := l.permitir(clienteDe(r))
		if !ok {
			segundos := int(math.Ceil(espera.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(segundos))
			renderizar(w, http.StatusTooManyRequests, "error", datosPagina{
				Titulo:  "Demasiadas peticiones",
				Mensaje: fmt.Sprintf("Vas muy rápido. Espera %d s y vuelve a intentarlo.", segundos),
			})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// clienteDe identifies the client behind a request. X-Real-IP is only
// trusted when the connection comes from a private or loopback address (the
// reverse proxy); anyone else could forge it. IPv6 clients are grouped by
// their /64, the block a single subscriber usually gets.
func clienteDe(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() {
		if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			ip = real.Unmap()
		}
	}
	if ip.Is6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}
