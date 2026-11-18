package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Search goes through Algolia, the engine behind the CeX website's own
// search, served from a CeX domain. That domain is a proxy that adds the
// Algolia credentials itself: requests without any key or application ID
// get the same results, so webye sends none and needs no configuration.

const (
	indiceAlgolia = "prod_cex_es"

	// The same page size the CeX website asks for, so browsing webye never
	// sends Algolia more requests than browsing CeX. Images load lazily
	// (loading="lazy"), so a long page costs nothing until it is scrolled.
	resultadosPorPagina = 170

	// The website's default filter: visible, for sale, priced and in stock.
	filtroBase = "boxVisibilityOnWeb=1 AND boxSaleAllowed=1 AND sellPrice > 0 AND (inStockStore=1 OR inStockOnline=1) AND (collectionQuantity>0 OR ecomQuantity>0)"
)

// baseAlgolia is a variable only so tests can point it to a local server.
var baseAlgolia = "https://search.webuy.io"

// Only the fields that are rendered: about 700 B per result instead of the
// several KB the website gets with every store and highlight.
var camposBusqueda = []string{
	"boxId", "boxName", "categoryFriendlyName",
	"sellPrice", "cashPriceCalculated", "exchangePriceCalculated",
	"imageUrls", "ecomQuantity", "collectionQuantity",
}

type producto struct {
	ID        string  `json:"boxId"`
	Nombre    string  `json:"boxName"`
	Categoria string  `json:"categoryFriendlyName"`
	Venta     float64 `json:"sellPrice"`
	Efectivo  float64 `json:"cashPriceCalculated"`
	Vale      float64 `json:"exchangePriceCalculated"`
	Imagenes  struct {
		Pequena string `json:"small"`
		Mediana string `json:"medium"`
		Grande  string `json:"large"`
	} `json:"imageUrls"`
	Online int `json:"ecomQuantity"`
	Tienda int `json:"collectionQuantity"`
}

type resultadoBusqueda struct {
	Total     int        `json:"nbHits"`
	Paginas   int        `json:"nbPages"`
	Pagina    int        `json:"page"`
	Productos []producto `json:"hits"`
}

// opcionOrden is one of the sort options of the CeX website. Each one is a
// replica of the main index named indiceAlgolia + "_" + Valor, as read from
// the website's own requests; a replica holds the same products, only in
// another order (measured: same nbHits in all seven).
type opcionOrden struct {
	Valor    string // ?sort= value and replica suffix; "" is relevance
	Etiqueta string // the website's own label
}

var opcionesOrden = []opcionOrden{
	{"", "Relevancia"},
	{"popularity_desc", "Mas populares"},
	{"price_asc", "Precios Menor - Mayor"},
	{"price_desc", "Precios Mayor - Menor"},
	{"box_name_asc", "Nombre del Productos a-z"},
	{"box_name_desc", "Nombre del Productos z-a"},
	{"rating_desc", "Mejor valorados"},
}

// ordenValido returns valor if it is one of the website's sort options and
// "" (relevance) otherwise, so no visitor can make webye query an
// arbitrary index.
func ordenValido(valor string) string {
	for _, o := range opcionesOrden {
		if o.Valor == valor {
			return valor
		}
	}
	return ""
}

func indicePara(orden string) string {
	if orden = ordenValido(orden); orden == "" {
		return indiceAlgolia
	}
	return indiceAlgolia + "_" + orden
}

// buscar returns one page (from 0) of results for consulta, in the given
// sort order ("" is relevance).
func buscar(ctx context.Context, consulta string, pagina int, orden string) (resultadoBusqueda, error) {
	r, _, err := consultarAlgolia(ctx, consulta, pagina, orden)
	return r, err
}

// normalizar makes "PS5", " ps5 " and "ps5" share one cache entry: Algolia
// ignores case and extra spaces anyway.
func normalizar(consulta string) string {
	return strings.ToLower(strings.Join(strings.Fields(consulta), " "))
}

// consultarAlgolia also returns the raw response, for the probe.
func consultarAlgolia(ctx context.Context, consulta string, pagina int, orden string) (resultadoBusqueda, respuesta, error) {
	consulta = normalizar(consulta)
	orden = ordenValido(orden)
	if consulta == "" || pagina < 0 {
		return resultadoBusqueda{}, respuesta{}, nil
	}
	// No userToken and no click analytics: the website sends both for
	// Algolia's analytics, webye sends neither.
	cuerpo, err := json.Marshal(map[string]any{
		"requests": []map[string]any{{
			"indexName":             indicePara(orden),
			"query":                 consulta,
			"hitsPerPage":           resultadosPorPagina,
			"page":                  pagina,
			"clickAnalytics":        false,
			"facets":                []string{},
			"attributesToRetrieve":  camposBusqueda,
			"attributesToHighlight": []string{},
			"filters":               filtroBase,
		}},
	})
	if err != nil {
		return resultadoBusqueda{}, respuesta{}, err
	}
	direccion := baseAlgolia + "/1/indexes/*/queries"

	// Each sort order is a different search with its own cache entry.
	clave := consulta + "|" + strconv.Itoa(pagina) + "|" + orden
	r, err := algolia.obtener(ctx, clave, ttlBusqueda, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, direccion, bytes.NewReader(cuerpo))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return resultadoBusqueda{}, respuesta{}, err
	}
	res, err := leerAlgolia(r)
	if err != nil {
		// The answer arrived but is not what webye expects: Algolia or the
		// CeX proxy changed. /salud reports it.
		algolia.fallo(err)
	}
	return res, r, err
}

func leerAlgolia(r respuesta) (resultadoBusqueda, error) {
	if r.Estado != http.StatusOK {
		return resultadoBusqueda{}, fmt.Errorf("Algolia answered %d", r.Estado)
	}
	var datos struct {
		Results []resultadoBusqueda `json:"results"`
	}
	if err := json.Unmarshal(r.Cuerpo, &datos); err != nil {
		return resultadoBusqueda{}, fmt.Errorf("unreadable Algolia response: %w", err)
	}
	if len(datos.Results) == 0 {
		return resultadoBusqueda{}, errors.New("Algolia response without results")
	}
	return datos.Results[0], nil
}
