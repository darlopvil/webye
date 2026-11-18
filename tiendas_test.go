package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A trimmed copy of a real neareststores answer: "4+" is what CeX sends
// for four or more, and the store without Click & Collect is real too.
const tiendasCeX = `{"response":{"ack":"Success","data":{"nearestStores":[` +
	`{"storeId":"1088","storeName":"Málaga Rosaleda","quantityOnHand":"4+","collectionQuantity":"4+","isAvailableForCollection":1,"distance":null,"holidayHours":null,"holidayTimings":null,"timings":{"open":{"wednesday":"10:00","sunday":"closed"},"close":{"wednesday":"22:00","sunday":"closed"}}},` +
	`{"storeId":"1090","storeName":"Terrassa Fontvella","quantityOnHand":"4","collectionQuantity":"0","isAvailableForCollection":0,"distance":null,"holidayHours":null,"holidayTimings":null,"timings":{"open":{"wednesday":"10:00"},"close":{"wednesday":"21:00"}}},` +
	`{"storeId":"1023","storeName":"Albacete Concepción","quantityOnHand":"1","collectionQuantity":"1","isAvailableForCollection":1,"distance":null,"holidayHours":null,"holidayTimings":null,"timings":{"open":{"wednesday":"10:00"},"close":{"wednesday":"21:00"}}}` +
	`]},"error":{"code":"","internal_message":"","moreInfo":[]}}}`

// wss2ConTiendas serves the product detail and the given stores answer,
// and keeps the path of every request.
func wss2ConTiendas(t *testing.T, tiendas http.HandlerFunc) *[]string {
	t.Helper()
	var rutas []string
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		rutas = append(rutas, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/neareststores") {
			if q := r.URL.Query(); q.Get("latitude") != "0" || q.Get("longitude") != "0" {
				t.Errorf("stores asked with a location: %s", r.URL.RawQuery)
			}
			tiendas(w, r)
			return
		}
		jsonDe(fichaIphone)(w, r)
	})
	// A Wednesday at 18:00 in Madrid.
	antes := ahoraTiendas
	ahoraTiendas = func() time.Time { return time.Date(2026, 11, 18, 18, 0, 0, 0, madrid) }
	t.Cleanup(func() { ahoraTiendas = antes })
	return &rutas
}

func contar(rutas []string, sufijo string) int {
	n := 0
	for _, r := range rutas {
		if strings.HasSuffix(r, sufijo) {
			n++
		}
	}
	return n
}

func TestAProductPageDoesNotAskForTheStores(t *testing.T) {
	rutas := wss2ConTiendas(t, jsonDe(tiendasCeX))
	html := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA").Body.String()
	if contar(*rutas, "/neareststores") != 0 {
		t.Fatal("the stores were asked for without the visitor asking")
	}
	for _, debe := range []string{
		`href="/product-detail/?id=SAPPIP17P256GNCLIBA&amp;stores=1"`,
		`data-tiendas="/product-detail/stores?id=SAPPIP17P256GNCLIBA"`,
		`aria-labelledby="titulo-tiendas" hidden>`,
		`<script src="` + estatico("tiendas.js") + `" defer></script>`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
}

func TestWithStoresThePanelComesDrawn(t *testing.T) {
	rutas := wss2ConTiendas(t, jsonDe(tiendasCeX))
	html := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA&stores=1").Body.String()
	if contar(*rutas, "/neareststores") != 1 || strings.Contains(html, `titulo-tiendas" hidden`) {
		t.Fatalf("panel not drawn: %v", *rutas)
	}
	for _, debe := range []string{
		`<span class="tienda-nombre">Albacete Concepción</span>`,
		// html/template writes "+" as "&#43;": the browser shows "4+".
		`<span class="tienda-unidades">4&#43; en stock</span>`,
		"Abierto hasta las 22:00", "Abierto hasta las 21:00",
		`<a class="panel-cerrar" href="/product-detail/?id=SAPPIP17P256GNCLIBA"`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	if n := strings.Count(html, "Click &amp; Recoge"); n != 2 {
		t.Errorf("Click & Recoge shown %d times, want 2 (Terrassa has none)", n)
	}
	if !(strings.Index(html, "Albacete") < strings.Index(html, "Málaga") && strings.Index(html, "Málaga") < strings.Index(html, "Terrassa")) {
		t.Error("stores not in alphabetical order")
	}
}

func TestTheFragmentIsTheListAloneAndIsCached(t *testing.T) {
	rutas := wss2ConTiendas(t, jsonDe(tiendasCeX))
	for i := 0; i < 2; i++ {
		w := pedirRuta(t, "/product-detail/stores?id=SAPPIP17P256GNCLIBA")
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "<html") || strings.Count(w.Body.String(), `class="tienda-nombre"`) != 3 {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
	}
	if n := contar(*rutas, "/neareststores"); n != 1 {
		t.Fatalf("%d requests to CeX for the same stores within 5 minutes, want 1", n)
	}
}

func TestStoresThatFailDoNotBreakThePage(t *testing.T) {
	wss2ConTiendas(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	w := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA&stores=1")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No se ha podido consultar el stock en tiendas") {
		t.Fatalf("product page: %d", w.Code)
	}
	if w := pedirRuta(t, "/product-detail/stores?id=SAPPIP17P256GNCLIBA"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("fragment: %d", w.Code)
	}
}

func TestNoStoreHasIt(t *testing.T) {
	wss2ConTiendas(t, jsonDe(`{"response":{"ack":"Success","data":null,"error":{}}}`))
	if html := pedirRuta(t, "/product-detail/stores?id=SAPPIP17P256GNCLIBA").Body.String(); !strings.Contains(html, "no lo tiene ninguna tienda") {
		t.Fatalf("got %s", html)
	}
}

func TestStoresOfAnInvalidProduct(t *testing.T) {
	wss2ConTiendas(t, jsonDe(tiendasCeX))
	if w := pedirRuta(t, "/product-detail/stores?id=../x"); w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}

func TestOpeningHours(t *testing.T) {
	semana := horarioCeX{
		Abre:   map[string]string{"wednesday": "10:00", "sunday": "closed"},
		Cierra: map[string]string{"wednesday": "22:00", "sunday": "closed"},
	}
	miercoles := func(h, m int) time.Time { return time.Date(2026, 11, 18, h, m, 0, 0, madrid) }
	domingo := time.Date(2026, 11, 22, 12, 0, 0, 0, madrid)
	casos := []struct {
		tienda tiendaCeX
		ahora  time.Time
		quiero string
	}{
		{tiendaCeX{Horario: semana}, miercoles(9, 30), "Abre hoy a las 10:00"},
		{tiendaCeX{Horario: semana}, miercoles(10, 0), "Abierto hasta las 22:00"},
		{tiendaCeX{Horario: semana}, miercoles(21, 59), "Abierto hasta las 22:00"},
		{tiendaCeX{Horario: semana}, miercoles(22, 0), "Cerrado ahora"},
		{tiendaCeX{Horario: semana}, domingo, "Cerrado hoy"},
		// Hours missing for the day, or a holiday whose shape is unknown.
		{tiendaCeX{Horario: semana}, time.Date(2026, 11, 19, 12, 0, 0, 0, madrid), ""},
		{tiendaCeX{Horario: semana, Festivos: []byte(`"10:00-14:00"`)}, miercoles(12, 0), ""},
		{tiendaCeX{Horario: semana, Festivos: []byte(`null`)}, miercoles(12, 0), "Abierto hasta las 22:00"},
	}
	for _, c := range casos {
		if got := horarioHoy(c.tienda, c.ahora); got != c.quiero {
			t.Errorf("%s: %q, want %q", c.ahora.Format("Mon 15:04"), got, c.quiero)
		}
	}
}

func TestTheScriptIsFreeSoftwareForLibreJS(t *testing.T) {
	js := string(estaticos["/static/tiendas.js"].contenido)
	if !strings.HasPrefix(js, "// @license magnet:?xt=urn:btih:0b31508aeb0634b347b8270c7bee4d411b5d4109&dn=agpl-3.0.txt AGPL-3.0\n") ||
		!strings.HasSuffix(js, "// @license-end\n") {
		t.Fatal("tiendas.js lacks the LibreJS license tags")
	}
	if tipo := pedirRuta(t, estatico("tiendas.js")).Header().Get("Content-Type"); !strings.HasPrefix(tipo, "text/javascript") {
		t.Fatalf("served as %q", tipo)
	}
}

func TestScriptsOnlyFromThisServer(t *testing.T) {
	if !strings.Contains(politicaContenido, "script-src 'self';") || strings.Contains(politicaContenido, "unsafe") {
		t.Fatalf("policy: %s", politicaContenido)
	}
}

func TestASoldOutProductHasNoStoresButton(t *testing.T) {
	wss2DePrueba(t, jsonDe(strings.Replace(fichaIphone, `"collectionQuantity":50`, `"collectionQuantity":0`, 1)))
	html := pedirRuta(t, "/product-detail/?id=SAPPIP17P256GNCLIBA").Body.String()
	if !strings.Contains(html, "sello-agotado") || strings.Contains(html, `id="ver-tiendas"`) {
		t.Fatal("a sold-out product should show Agotado and no stores button")
	}
}
