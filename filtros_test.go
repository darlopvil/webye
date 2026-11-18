package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type consultaRecibida struct {
	IndexName      string     `json:"indexName"`
	HitsPerPage    int        `json:"hitsPerPage"`
	Facets         []string   `json:"facets"`
	FacetFilters   [][]string `json:"facetFilters"`
	NumericFilters []string   `json:"numericFilters"`
	Filters        string     `json:"filters"`
	Analytics      *bool      `json:"analytics"`
}

// algoliaConFacetas answers like Algolia: one result per query. The main
// query gets hits and the facets given; each count query gets the counts
// given for its own list. It keeps every request it receives.
func algoliaConFacetas(t *testing.T, hits int, facetas, recuentos map[string]map[string]int) *[][]consultaRecibida {
	t.Helper()
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	var recibidas [][]consultaRecibida
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		var cuerpo struct{ Requests []consultaRecibida }
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &cuerpo); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		recibidas = append(recibidas, cuerpo.Requests)
		var resultados []map[string]any
		for i, c := range cuerpo.Requests {
			if i == 0 {
				var productos []map[string]any
				for n := 0; n < hits; n++ {
					productos = append(productos, map[string]any{"boxId": fmt.Sprint("ID", n), "boxName": "Juego", "sellPrice": 10})
				}
				resultados = append(resultados, map[string]any{"nbHits": hits, "nbPages": 1, "page": 0, "hits": productos, "facets": facetas})
				continue
			}
			resultados = append(resultados, map[string]any{"nbHits": 0, "hits": []any{}, "facets": map[string]any{c.Facets[0]: recuentos[c.Facets[0]]}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": resultados})
	})
	return &recibidas
}

func TestACeXFilteredLinkSendsWhatTheWebsiteSends(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	// A real link copied from the website, with "Mostrar productos
	// agotados" on, one store, a PEGI rating and a minimum price.
	w := pedirRuta(t, "/search/?stext=tomb+raider&stores=A+Coru%C3%B1a+Marineda+City&sellPrice=15:&Clasificaci%C3%B3n+PEGI=18%2B&availability=allStock")
	if w.Code != http.StatusOK || len(*recibidas) != 1 {
		t.Fatalf("status %d, %d requests", w.Code, len(*recibidas))
	}
	c := (*recibidas)[0]
	if len(c) != 3 {
		t.Fatalf("want the main query and one count query per list, got %d queries", len(c))
	}
	principal := c[0]
	if !reflect.DeepEqual(principal.FacetFilters, [][]string{{"Clasificación PEGI:18+"}, {"stores:A Coruña Marineda City"}}) {
		t.Errorf("facetFilters %v", principal.FacetFilters)
	}
	if !reflect.DeepEqual(principal.NumericFilters, []string{"sellPrice>=15"}) || principal.Filters != filtroVisible {
		t.Errorf("numericFilters %v, filters %q", principal.NumericFilters, principal.Filters)
	}
	if !reflect.DeepEqual(principal.Facets, []string{"*"}) {
		t.Errorf("facets %v", principal.Facets)
	}
	// Each count query leaves its own list out, keeps the rest, asks for
	// no products and no analytics. No count query for the price.
	quiero := map[string][][]string{
		"Clasificación PEGI": {{"stores:A Coruña Marineda City"}},
		"stores":             {{"Clasificación PEGI:18+"}},
	}
	for _, r := range c[1:] {
		if len(r.Facets) != 1 || !reflect.DeepEqual(r.FacetFilters, quiero[r.Facets[0]]) {
			t.Errorf("count query for %v filters %v", r.Facets, r.FacetFilters)
		}
		if r.HitsPerPage != 0 || r.Analytics == nil || *r.Analytics || !reflect.DeepEqual(r.NumericFilters, []string{"sellPrice>=15"}) {
			t.Errorf("count query %+v", r)
		}
	}
}

func TestValuesOfOneListAreAlternatives(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=tomb+raider&categoryFriendlyName=Blu-Ray+4K~Blu-Ray+Peliculas&sellPrice=10:20")
	p := (*recibidas)[0][0]
	if !reflect.DeepEqual(p.FacetFilters, [][]string{{"categoryFriendlyName:Blu-Ray 4K", "categoryFriendlyName:Blu-Ray Peliculas"}}) {
		t.Errorf("facetFilters %v", p.FacetFilters)
	}
	if !reflect.DeepEqual(p.NumericFilters, []string{"sellPrice>=10", "sellPrice<=20"}) || p.Filters != filtroBase {
		t.Errorf("numericFilters %v, filters %q", p.NumericFilters, p.Filters)
	}
}

func TestNoFiltersIsStillOneQuery(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4")
	if c := (*recibidas)[0]; len(c) != 1 || c[0].FacetFilters != nil || c[0].NumericFilters != nil {
		t.Fatalf("got %+v", c)
	}
}

func TestTheFormIsRedirectedToTheWebsiteShape(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	casos := map[string]string{
		"/search/?stext=ps4&sort=price_asc&availability=allStock&precioMin=9,5&precioMax=&stores=Madrid+Islazul&stores=A+Coru%C3%B1a":
		/* → */ "/search/?stext=ps4&sort=price_asc&availability=allStock&sellPrice=9.5:&stores=A+Coru%C3%B1a~Madrid+Islazul",
		// Nothing filled in: back to the plain search.
		"/search/?stext=ps4&precioMin=&precioMax=": "/search/?stext=ps4",
		// The minimum above the maximum is taken as swapped.
		"/search/?stext=ps4&precioMin=50&precioMax=10&page=3": "/search/?stext=ps4&sellPrice=10:50",
	}
	for desde, hacia := range casos {
		w := pedirRuta(t, desde)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != hacia {
			t.Errorf("%s: %d to %q, want %q", desde, w.Code, w.Header().Get("Location"), hacia)
		}
		// The target is already in shape: no second redirect.
		if w := pedirRuta(t, hacia); w.Code != http.StatusOK {
			t.Errorf("%s: %d", hacia, w.Code)
		}
	}
	if len(*recibidas) != len(casos) {
		t.Errorf("the redirects themselves must not reach CeX: %d requests for %d searches", len(*recibidas), len(casos))
	}
}

func TestOnlyTheWebsitesFiltersAreAccepted(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4&boxBuyAllowed=0&categoryId=1001&scId=1&stores=-Madrid&sellPrice=abc:-5&availability=yes&Grado%3A=A")
	if c := (*recibidas)[0]; len(c) != 1 || c[0].FacetFilters != nil || c[0].NumericFilters != nil || c[0].Filters != filtroBase {
		t.Fatalf("junk reached Algolia: %+v", c)
	}
}

func TestAtMostThirteenLists(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	ruta := "/search/?stext=ps4"
	for i := 0; i < maxGrupos+5; i++ {
		ruta += fmt.Sprintf("&Atributo%02d=x", i)
	}
	pedirRuta(t, ruta)
	if c := (*recibidas)[0]; len(c) != 1+maxGrupos || len(c[0].FacetFilters) != maxGrupos {
		t.Fatalf("%d queries, %d groups", len(c), len(c[0].FacetFilters))
	}
}

func TestSameFiltersInAnotherOrderShareTheCache(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4&stores=A~B&sellPrice=10:")
	pedirRuta(t, "/search/?stext=ps4&sellPrice=10:&stores=B~A")
	pedirRuta(t, "/search/?stext=ps4&stores=A")
	if len(*recibidas) != 2 {
		t.Fatalf("%d requests, want 2", len(*recibidas))
	}
}

func TestThePanelShowsTheWebsitesListsInItsOrder(t *testing.T) {
	facetas := map[string]map[string]int{
		"stores":               {"Madrid Islazul": 3, "Albacete Concepción": 2, "Alcalá Magna": 1},
		"categoryFriendlyName": {"PS4 Juegos": 5},
		"Clasificación PEGI":   {"18+": 4, "3+": 1, "12+": 2},
		"Grado":                {"B": 1},
		"categoryId":           {"1001": 5},
		"scId":                 {"1": 5},
		"availability":         {"Disponible online": 5},
	}
	recuentos := map[string]map[string]int{"stores": {"Madrid Islazul": 9, "Albacete Concepción": 7, "Alcalá Magna": 1}}
	algoliaConFacetas(t, 5, facetas, recuentos)
	html := pedirRuta(t, "/search/?stext=ps4&sort=price_asc&stores=Madrid+Islazul").Body.String()

	orden := []string{"<summary>Tiendas", "<summary>Por Categoria", "<summary>Clasificación PEGI", "<summary>Grado"}
	ultimo := -1
	for _, s := range orden {
		i := strings.Index(html, s)
		if i < ultimo {
			t.Errorf("%q out of order or missing", s)
		}
		ultimo = i
	}
	for _, interno := range []string{"categoryId", "scId", "Disponible online"} {
		if strings.Contains(html, interno) {
			t.Errorf("internal list %q shown", interno)
		}
	}
	for _, debe := range []string{
		`<details class="filtros" open>`,
		`<details class="grupo-filtro" open>`,
		// Counts of the list in use come from its own query.
		`value="Madrid Islazul" checked> <span>Madrid Islazul <span class="tenue">(9)</span>`,
		`value="Albacete Concepción"> <span>Albacete Concepción <span class="tenue">(7)</span>`,
		`<input type="hidden" name="sort" value="price_asc">`,
		`<a href="/search/?stext=ps4&amp;sort=price_asc">Quitar filtros</a>`,
		// The sort links keep the filters.
		`<a href="/search/?stext=ps4&amp;sort=price_desc&amp;stores=Madrid&#43;Islazul">Precios Mayor - Menor</a>`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	// Ticked values first, then alphabetical without minding accents, and
	// numbers by value.
	for _, par := range [][2]string{{"Madrid Islazul", "Albacete Concepción"}, {"Albacete Concepción", "Alcalá Magna"}, {`"3+"`, `"12+"`}, {`"12+"`, `"18+"`}} {
		if strings.Index(html, par[0]) > strings.Index(html, par[1]) {
			t.Errorf("%s should come before %s", par[0], par[1])
		}
	}
}

func TestTickedValuesStayWhenNothingIsLeft(t *testing.T) {
	algoliaConFacetas(t, 0, map[string]map[string]int{}, nil)
	html := pedirRuta(t, "/search/?stext=ps4&Grado=Z").Body.String()
	for _, debe := range []string{"Nada con estos filtros", `value="Z" checked`, "Quitar filtros"} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	if strings.Contains(html, `class="ordenes"`) {
		t.Error("sort links without results")
	}
}

func TestNoPanelForAnEmptySearchWithoutFilters(t *testing.T) {
	algoliaConFacetas(t, 0, nil, nil)
	if html := pedirRuta(t, "/search/?stext=zzzz").Body.String(); strings.Contains(html, `class="filtros"`) {
		t.Error("filter panel with nothing to filter")
	}
}

func TestSoldOutProductsAreMarked(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(`{"results":[{"nbHits":1,"nbPages":1,"page":0,"hits":[{"boxId":"X","boxName":"Juego","sellPrice":3,"inStockOnline":0,"inStockStore":0}]}]}`))
	if html := pedirRuta(t, "/search/?stext=juego&availability=allStock").Body.String(); !strings.Contains(html, `<span class="sello sello-agotado">Agotado</span>`) {
		t.Error("sold-out product not marked")
	}
}

func TestRefusedFiltersAreNotACeXFailure(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"invalid filters"}`))
	})
	if w := pedirRuta(t, "/search/?stext=ps4&Grado=A"); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
	if w := pedirRuta(t, "/salud?destino=algolia"); w.Code != http.StatusOK {
		t.Fatalf("/salud turned red over a visitor's filters: %s", w.Body.String())
	}
	// Without filters, the same answer means something changed at CeX.
	pedirRuta(t, "/search/?stext=ps4")
	if w := pedirRuta(t, "/salud?destino=algolia"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a 400 without filters should turn /salud red, got %d", w.Code)
	}
}

func TestNaturalOrder(t *testing.T) {
	casos := [][2]string{
		{"3+", "12+"}, {"Albacete", "Alcalá"}, {"alcalá", "Alcorcón"},
		{"Color de la Consola", "Color Principal"}, {"PS2", "PS10"}, {"a", "B"},
	}
	for _, c := range casos {
		if ordenNatural(c[0], c[1]) >= 0 || ordenNatural(c[1], c[0]) <= 0 {
			t.Errorf("%q should come before %q", c[0], c[1])
		}
	}
	if ordenNatural("Á", "A") == 0 {
		t.Error("different values must never compare equal")
	}
}

func TestPrices(t *testing.T) {
	casos := map[string]string{"15": "15", "9,5": "9.5", " 9.50 ": "9.5", "0": "", "-3": "", "abc": "", "Inf": "", "NaN": "", "": ""}
	for entrada, quiero := range casos {
		if got := leerPrecio(entrada); got != quiero {
			t.Errorf("leerPrecio(%q) = %q, want %q", entrada, got, quiero)
		}
	}
	f := leerFiltros(url.Values{"sellPrice": {":20"}})
	if f.Precio.Min != "" || f.Precio.Max != "20" || f.parametros() != "&sellPrice=:20" {
		t.Errorf("open minimum: %+v", f)
	}
}
