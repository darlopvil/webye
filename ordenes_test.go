package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// algoliaQueRecuerda answers every search and keeps the index each one asked for.
func algoliaQueRecuerda(t *testing.T, indices *[]string) {
	t.Helper()
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		var cuerpo struct {
			Requests []struct {
				IndexName string `json:"indexName"`
			} `json:"requests"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &cuerpo)
		*indices = append(*indices, cuerpo.Requests[0].IndexName)
		jsonDe(respuestaPaginada(1182, 6, 1))(w, r)
	})
}

func TestEverySortOptionUsesItsCeXReplica(t *testing.T) {
	var indices []string
	algoliaQueRecuerda(t, &indices)
	quiero := map[string]string{
		"":                "prod_cex_es",
		"popularity_desc": "prod_cex_es_popularity_desc",
		"price_asc":       "prod_cex_es_price_asc",
		"price_desc":      "prod_cex_es_price_desc",
		"box_name_asc":    "prod_cex_es_box_name_asc",
		"box_name_desc":   "prod_cex_es_box_name_desc",
		"rating_desc":     "prod_cex_es_rating_desc",
	}
	for orden, indice := range quiero {
		indices = nil
		pedirRuta(t, "/search/?stext=iphone&sort="+orden)
		if len(indices) != 1 || indices[0] != indice {
			t.Errorf("sort=%q asked for %v, want %s", orden, indices, indice)
		}
	}
}

func TestUnknownSortFallsBackToRelevance(t *testing.T) {
	var indices []string
	algoliaQueRecuerda(t, &indices)
	for _, orden := range []string{"price", "..%2F..%2F1%2Fkeys", "prod_cex_es_price_asc", "PRICE_ASC"} {
		if w := pedirRuta(t, "/search/?stext=iphone&sort="+orden); w.Code != http.StatusOK {
			t.Errorf("sort=%q: status %d", orden, w.Code)
		}
	}
	// All of them are the relevance search, so they share one cache entry:
	// junk values cannot fill the cache or reach Algolia more than once.
	if len(indices) != 1 || indices[0] != "prod_cex_es" {
		t.Fatalf("asked Algolia for %v", indices)
	}
}

func TestSortLinksAndPaginationKeepTheOrder(t *testing.T) {
	var indices []string
	algoliaQueRecuerda(t, &indices)
	html := pedirRuta(t, "/search/?stext=iphone&sort=price_asc&page=2").Body.String()
	for _, debe := range []string{
		// The current order is marked and not a link.
		`<span class="actual" aria-current="true">Precios Menor - Mayor</span>`,
		// The other orders go back to page 1; relevance needs no sort.
		`<a href="/search/?stext=iphone">Relevancia</a>`,
		`<a href="/search/?stext=iphone&amp;sort=price_desc">Precios Mayor - Menor</a>`,
		`<a href="/search/?stext=iphone&amp;sort=rating_desc">Mejor valorados</a>`,
		// The pages keep the order.
		`href="/search/?stext=iphone&amp;sort=price_asc" rel="prev"`,
		`href="/search/?stext=iphone&amp;sort=price_asc&amp;page=3" rel="next"`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	if strings.Contains(html, `>Precios Menor - Mayor</a>`) {
		t.Error("the current order should not be a link")
	}
}

func TestEachOrderIsCachedOnItsOwn(t *testing.T) {
	var indices []string
	algoliaQueRecuerda(t, &indices)
	for i := 0; i < 2; i++ {
		pedirRuta(t, "/search/?stext=iphone")
		pedirRuta(t, "/search/?stext=iphone&sort=price_asc")
	}
	if len(indices) != 2 {
		t.Fatalf("want one request per order, got %v", indices)
	}
}

func TestNoSortLinksWithoutResults(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(`{"results":[{"nbHits":0,"nbPages":0,"page":0,"hits":[]}]}`))
	if html := pedirRuta(t, "/search/?stext=zzzz").Body.String(); strings.Contains(html, `class="ordenes"`) {
		t.Error("sort links shown for an empty search")
	}
}
