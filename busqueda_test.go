package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// algoliaDePrueba points the search at a local server for the duration of a
// test and returns what that server received.
func algoliaDePrueba(t *testing.T, h http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	base, guard := baseAlgolia, algolia
	baseAlgolia = srv.URL
	algolia = nuevoGuardian("algolia", "application/json", nuevaCache(cacheMaxima))
	t.Cleanup(func() { baseAlgolia, algolia = base, guard })
	return &llamadas
}

const respuestaAlgolia = `{"results":[{"nbHits":86,"nbPages":1,"page":0,"hits":[{"boxId":"5021290074811","boxName":"Rise of the Tomb Raider","categoryFriendlyName":"PS4 Juegos","sellPrice":15,"cashPriceCalculated":6,"exchangePriceCalculated":9,"imageUrls":{"small":"https://es.static.webuy.com/product_images/Juegos/PS4 Juegos/5021290074811_s.jpg","medium":"m","large":"l"},"ecomQuantity":50,"collectionQuantity":50,"objectID":"5021290074811"}]}]}`

func TestSearchRequestIsSlimAndAnonymous(t *testing.T) {
	var cuerpo map[string][]map[string]any
	var consulta string
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		consulta = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &cuerpo)
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.Write([]byte(respuestaAlgolia))
	})
	res, err := buscar(context.Background(), "  Tomb   RAIDER ", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if consulta != "" {
		t.Fatalf("no key or application ID should be sent, got query string %q", consulta)
	}
	p := cuerpo["requests"][0]
	esperado := map[string]any{
		"indexName": indiceAlgolia, "query": "tomb raider", "hitsPerPage": float64(resultadosPorPagina),
		"page": float64(0), "clickAnalytics": false, "filters": filtroBase,
	}
	for k, v := range esperado {
		if p[k] != v {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}
	for _, k := range []string{"facets", "attributesToHighlight"} {
		if l, ok := p[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s should be an empty list, got %v", k, p[k])
		}
	}
	if _, ok := p["userToken"]; ok {
		t.Error("userToken must never be sent")
	}
	if len(p["attributesToRetrieve"].([]any)) != len(camposBusqueda) {
		t.Error("only the rendered fields must be requested")
	}
	if res.Total != 86 || len(res.Productos) != 1 {
		t.Fatalf("parsed %+v", res)
	}
	pr := res.Productos[0]
	if pr.ID != "5021290074811" || pr.Venta != 15 || pr.Efectivo != 6 || pr.Vale != 9 || pr.Online != 50 || pr.Imagenes.Mediana != "m" {
		t.Fatalf("parsed product %+v", pr)
	}
}

func TestSameSearchDifferentSpellingIsOneRequest(t *testing.T) {
	n := algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respuestaAlgolia))
	})
	for _, q := range []string{"PS5", " ps5 ", "ps5"} {
		if _, err := buscar(context.Background(), q, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	buscar(context.Background(), "ps5", 1, "")
	if n.Load() != 2 {
		t.Fatalf("3 spellings of one search plus page 2 made %d requests, want 2", n.Load())
	}
}

func TestEmptySearchMakesNoRequest(t *testing.T) {
	n := algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {})
	if res, err := buscar(context.Background(), "   ", 0, ""); err != nil || res.Total != 0 {
		t.Fatalf("empty search: %+v, %v", res, err)
	}
	if n.Load() != 0 {
		t.Fatalf("%d requests left for an empty search", n.Load())
	}
}

func TestBlockIsReported(t *testing.T) {
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("<title>Attention Required! | Cloudflare</title>"))
	})
	if _, err := buscar(context.Background(), "ps5", 0, ""); !errors.Is(err, errBloqueo) {
		t.Fatalf("Cloudflare's HTML 403 should be a block, got %v", err)
	}
}
