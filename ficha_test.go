package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// wss2DePrueba points product details at a local server during a test.
func wss2DePrueba(t *testing.T, h http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	base, guard := baseWss2, wss2
	baseWss2 = srv.URL
	wss2 = nuevoGuardian("wss2", "application/json", nuevaCache(cacheMaxima))
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	t.Cleanup(func() { baseWss2, wss2 = base, guard })
	return &llamadas
}

// A trimmed copy of a real wss2 response for an iPhone.
const fichaIphone = `{"response":{"ack":"Success","data":{"boxDetails":[{"boxId":"SAPPIP17P256GNCLIBA","boxName":"Apple iPhone 17 Pro (A3523) 256GB Naranja Cósmico, Libre A","categoryFriendlyName":"Moviles - iPhone","superCatFriendlyName":"Móviles","cashPrice":737,"exchangePrice":814,"sellPrice":1100,"firstPrice":1250,"previousPrice":1150,"lastPriceUpdatedDate":"2026-10-20 01:00:06","boxRating":4.8,"collectionQuantity":50,"ecomQuantityOnHand":0,"imageUrls":{"large":"l","medium":"m","small":"s"},"boxDescription":"<p>El iPhone 17 Pro.</p> <ul> <h4>Detalles</h4> <ul> <li>Pantalla: 6.3 pulgadas</li> </ul><script>alert(1)</script>","attributeInfo":[{"attributeFriendlyName":"Capacidad","attributeValue":["256GB"],"showOnPDP":1},{"attributeFriendlyName":"Interno","attributeValue":["x"],"showOnPDP":0},{"attributeFriendlyName":"Ranuras tarjetas SIM","attributeValue":[1],"showOnPDP":1}]}],"masterBoxDetails":null},"error":{"code":"","internal_message":"","moreInfo":[]}}}`

func jsonDe(cuerpo string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.Write([]byte(cuerpo))
	}
}

func TestProductPageShowsEverything(t *testing.T) {
	var ruta string
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		ruta = r.URL.Path
		jsonDe(fichaIphone)(w, r)
	})
	w := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA&categoryName=MOVILES&queryID=ABC&position=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ruta != "/v3/boxes/SAPPIP17P256GNCLIBA/detail" {
		t.Fatalf("asked wss2 for %s", ruta)
	}
	html := w.Body.String()
	for _, debe := range []string{
		"Apple iPhone 17 Pro (A3523) 256GB Naranja Cósmico, Libre A",
		"Móviles › Moviles - iPhone", "★ 4,8",
		"1.100 €", "737 €", "814 €",
		"Antes: 1.150 €", "Salió a: 1.250 €", "Último cambio de precio: 20/10/2026",
		"Recogida en tienda", "<th scope=\"row\">Capacidad</th><td>256GB</td>",
		"<td>1</td>", "<h4>Detalles</h4>",
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	for _, no := range []string{"En stock online", "Interno", "<script>", "alert(1)"} {
		if strings.Contains(html, no) {
			t.Errorf("should not contain %q", no)
		}
	}
}

func TestProductPageWithoutSlashWorksToo(t *testing.T) {
	wss2DePrueba(t, jsonDe(fichaIphone))
	if w := pedirRuta(t, "/product-detail?id=SAPPIP17P256GNCLIBA"); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestBadOrUnknownProductIsA404(t *testing.T) {
	// The real wss2 answer for an unknown box: a 200 with "Success" and no data.
	n := wss2DePrueba(t, jsonDe(`{"response":{"ack":"Success","data":null,"error":{"code":"","internal_message":"","moreInfo":[]}}}`))
	for _, ruta := range []string{"/product-detail/?id=", "/product-detail/?id=../../etc", "/product-detail/?id=a%20b"} {
		if w := pedirRuta(t, ruta); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", ruta, w.Code)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("invalid IDs reached wss2 %d times", n.Load())
	}
	w := pedirRuta(t, "/product-detail/?id=NOEXISTE123")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Producto no encontrado") {
		t.Fatalf("unknown product: status %d", w.Code)
	}
}

func TestProductNotFoundStatusFromWss2(t *testing.T) {
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
	})
	if w := pedirRuta(t, "/product-detail/?id=NOEXISTE123"); w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}

func TestProductPageBlockIsA503(t *testing.T) {
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
	})
	if w := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestProductDetailsAreCached(t *testing.T) {
	n := wss2DePrueba(t, jsonDe(fichaIphone))
	h := rutas()
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/product-detail/?id=SAPPIP17P256GNCLIBA", nil))
	}
	if n.Load() != 1 {
		t.Fatalf("3 views of one product made %d requests, want 1", n.Load())
	}
}
