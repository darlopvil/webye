package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pedirRuta sends one request through the routes of the site, as a browser
// would.
func pedirRuta(t *testing.T, ruta string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	rutas().ServeHTTP(w, httptest.NewRequest(http.MethodGet, ruta, nil))
	return w
}

func respuestaPaginada(total, paginas, pagina int) string {
	return fmt.Sprintf(`{"results":[{"nbHits":%d,"nbPages":%d,"page":%d,"hits":[{"boxId":"5021290074811","boxName":"Rise of the Tomb Raider","categoryFriendlyName":"PS4 Juegos","sellPrice":1100,"cashPriceCalculated":6,"exchangePriceCalculated":9.5,"imageUrls":{"small":"s","medium":"m","large":"l"},"ecomQuantity":50,"collectionQuantity":0}]}]}`, total, paginas, pagina)
}

func TestResultsPageShowsProductsAndPrices(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respuestaPaginada(86, 1, 0)))
	})
	for _, ruta := range []string{"/search/?stext=Tomb+Raider", "/search?stext=tomb%20raider"} {
		w := pedirRuta(t, ruta)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", ruta, w.Code)
		}
		html := w.Body.String()
		for _, debe := range []string{
			"Rise of the Tomb Raider", "PS4 Juegos",
			"1.100 €", "6 €", "9,50 €",
			`href="/product-detail/?id=5021290074811"`,
			">Online<", "«tomb raider»", `value="tomb raider"`,
		} {
			if !strings.Contains(html, debe) {
				t.Errorf("%s: missing %q", ruta, debe)
			}
		}
		if strings.Contains(html, "En tienda") {
			t.Errorf("%s: shows in-store stock with collectionQuantity 0", ruta)
		}
		if strings.Contains(html, "paginacion") {
			t.Errorf("%s: a single page needs no pagination", ruta)
		}
	}
}

func TestPaginationFollowsCeXParameters(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	var paginaPedida string
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		paginaPedida = string(cuerpo)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respuestaPaginada(1182, 6, 1)))
	})
	w := pedirRuta(t, "/search/?stext=iphone&page=2")
	html := w.Body.String()
	if !strings.Contains(paginaPedida, `"page":1`) {
		t.Fatalf("CeX's page=2 is Algolia's page 1, request was %s", paginaPedida)
	}
	for _, debe := range []string{
		`<span class="actual" aria-current="page">2</span>`,
		`href="/search/?stext=iphone" rel="prev"`,
		`href="/search/?stext=iphone&amp;page=3" rel="next"`,
		`href="/search/?stext=iphone&amp;page=6"`,
		"primeros 1.000", "<strong>1.182</strong>",
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
}

func TestEmptySearchGoesHome(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	n := algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {})
	w := pedirRuta(t, "/search/?stext=+")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("got %d to %q", w.Code, w.Header().Get("Location"))
	}
	if n.Load() != 0 {
		t.Fatal("an empty search reached Algolia")
	}
}

func TestBlockShowsAnExplanationNotA500(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	w := pedirRuta(t, "/search/?stext=ps5")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "respiro") {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

func TestSearchRouteIsRateLimited(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respuestaPaginada(1, 1, 0)))
	})
	h := rutas()
	var ultimo int
	for i := 0; i <= rafagaPaginas; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/search/?stext=ps5", nil))
		ultimo = w.Code
	}
	if ultimo != http.StatusTooManyRequests {
		t.Fatalf("request %d got %d, want 429", rafagaPaginas+1, ultimo)
	}
}

func TestSpanishNumbers(t *testing.T) {
	casos := map[float64]string{0: "0 €", 6: "6 €", 9.5: "9,50 €", 15.05: "15,05 €", 1100: "1.100 €", 1234567.89: "1.234.567,89 €"}
	for v, quiero := range casos {
		if got := euros(v); got != quiero {
			t.Errorf("euros(%v) = %q, want %q", v, got, quiero)
		}
	}
	if miles(1182) != "1.182" || miles(999) != "999" || miles(1000) != "1.000" {
		t.Error("miles")
	}
}
