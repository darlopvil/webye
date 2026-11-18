package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// imagenesDePrueba points the image proxy at a local server during a test
// and returns the escaped paths it was asked for.
func imagenesDePrueba(t *testing.T, h http.HandlerFunc) (*atomic.Int32, *[]string) {
	t.Helper()
	var llamadas atomic.Int32
	var rutas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		rutas = append(rutas, r.URL.EscapedPath())
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	base, guard := baseImagenes, imagenes
	baseImagenes = srv.URL
	imagenes = conRedirecciones(nuevoGuardian("imagenes", "image/", nuevaCache(cacheMaxima)))
	limiteImagenes = nuevoLimitador(2*resultadosPorPagina, resultadosPorPagina/60.0)
	t.Cleanup(func() { baseImagenes, imagenes = base, guard })
	return &llamadas, &rutas
}

func jpeg(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=10800")
	w.Write([]byte("\xff\xd8jpeg"))
}

func TestFotoMapsOnlyCeXProductImages(t *testing.T) {
	casos := map[string]string{
		"https://es.static.webuy.com/product_images/Móviles/Moviles - iPhone/SAPPIP17P256GNCLIBA_m.jpg": "/img/M%C3%B3viles/Moviles%20-%20iPhone/SAPPIP17P256GNCLIBA_m.jpg",
		"https://es.static.webuy.com/product_images/Juegos/PS4 Juegos/5021290074811_l.jpg":              "/img/Juegos/PS4%20Juegos/5021290074811_l.jpg",
		"https://tracker.example/product_images/a.jpg":                                                  "",
		"http://es.static.webuy.com/product_images/a.jpg":                                               "",
		"https://es.static.webuy.com/otra/a.jpg":                                                        "",
		"https://es.static.webuy.com/product_images/../secreto.jpg":                                     "",
		"https://es.static.webuy.com/product_images/a.svg":                                              "",
		"": "",
	}
	for origen, quiero := range casos {
		if got := foto(origen); got != quiero {
			t.Errorf("foto(%q) = %q, want %q", origen, got, quiero)
		}
	}
}

func TestImageProxyFetchesFromCeXFolderAndCaches(t *testing.T) {
	n, pedidas := imagenesDePrueba(t, jpeg)
	h := rutas()
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/img/M%C3%B3viles/Moviles%20-%20iPhone/X_m.jpg", nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.String() != "\xff\xd8jpeg" {
			t.Fatalf("got %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	if n.Load() != 1 {
		t.Fatalf("3 views of one photo made %d requests, want 1 (max-age)", n.Load())
	}
	if (*pedidas)[0] != "/product_images/M%C3%B3viles/Moviles%20-%20iPhone/X_m.jpg" {
		t.Fatalf("asked upstream for %s", (*pedidas)[0])
	}
}

func TestImageProxyRejectsAnythingElse(t *testing.T) {
	n, _ := imagenesDePrueba(t, jpeg)
	h := rutas()
	for _, ruta := range []string{"/img/", "/img/a.txt", "/img/a//b.jpg", "/img/..%2f..%2fetc%2fpasswd.jpg", "/img/../static/estilo.css", "/img/x.svg"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ruta, nil))
		if w.Code == http.StatusOK && strings.HasPrefix(ruta, "/img/") && w.Header().Get("Content-Type") == "image/jpeg" {
			t.Errorf("%s was served", ruta)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("invalid paths reached the image host %d times", n.Load())
	}
}

func TestImageProxyPassesOnMissingAndBlocks(t *testing.T) {
	estado := http.StatusNotFound
	imagenesDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if estado == http.StatusOK {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<title>Just a moment...</title>"))
			return
		}
		w.WriteHeader(estado)
	})
	if w := pedirRuta(t, "/img/a/b.jpg"); w.Code != http.StatusNotFound {
		t.Fatalf("missing photo: %d", w.Code)
	}
	estado = http.StatusOK
	if w := pedirRuta(t, "/img/a/c.jpg"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("challenge page instead of a photo: %d", w.Code)
	}
}

func TestImageProxyIsRateLimited(t *testing.T) {
	imagenesDePrueba(t, jpeg)
	h := rutas()
	var ultimo int
	for i := 0; i <= 2*resultadosPorPagina; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/img/a/x.jpg", nil))
		ultimo = w.Code
	}
	if ultimo != http.StatusTooManyRequests {
		t.Fatalf("photo %d got %d, want 429", 2*resultadosPorPagina+1, ultimo)
	}
}

func TestPagesShowPhotosThroughTheProxy(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(strings.Replace(respuestaPaginada(1, 1, 0), `"medium":"m"`, `"medium":"https://es.static.webuy.com/product_images/Juegos/PS4 Juegos/5021290074811_m.jpg"`, 1)))
	html := pedirRuta(t, "/search/?stext=x").Body.String()
	if !strings.Contains(html, `<img src="/img/Juegos/PS4%20Juegos/5021290074811_m.jpg" alt="" width="190" height="240" loading="lazy"`) {
		t.Error("results page has no lazy medium photo through the proxy")
	}
	wss2DePrueba(t, jsonDe(strings.Replace(fichaIphone, `"large":"l"`, `"large":"https://es.static.webuy.com/product_images/Móviles/Moviles - iPhone/SAPPIP17P256GNCLIBA_l.jpg"`, 1)))
	html = pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA").Body.String()
	if !strings.Contains(html, `<img src="/img/M%C3%B3viles/Moviles%20-%20iPhone/SAPPIP17P256GNCLIBA_l.jpg"`) {
		t.Error("product page has no large photo through the proxy")
	}
	if strings.Contains(html, "es.static.webuy.com") {
		t.Error("a page links straight to CeX's image host")
	}
}

func TestMissingPhotoShowsOurPlaceholderAndIsCached(t *testing.T) {
	// What es.static.webuy.com really answers for a product without a photo.
	n, _ := imagenesDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "max-age=300, public")
		w.Header().Set("Location", "https://es.static.webuy.com/images/default/defimg1_m.jpg")
		w.WriteHeader(http.StatusFound)
	})
	h := rutas()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/img/Juegos/PS1%20Juegos/SPS1G624AB_m.jpg", nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(w.Body.String(), "SIN FOTO") {
			t.Fatalf("got %d %q", w.Code, w.Header().Get("Content-Type"))
		}
	}
	if n.Load() != 1 {
		t.Fatalf("a missing photo was asked for %d times within CeX's max-age, want 1", n.Load())
	}
}
