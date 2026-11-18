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

	// The website's filter: visible, for sale and priced. With "Mostrar
	// productos agotados" off, which is the default, it also asks for stock.
	filtroVisible = "boxVisibilityOnWeb=1 AND boxSaleAllowed=1 AND sellPrice > 0"
	filtroBase    = filtroVisible + " AND (inStockStore=1 OR inStockOnline=1) AND (collectionQuantity>0 OR ecomQuantity>0)"

	// The website asks for up to 1000 values per filter list. The longest
	// list measured is the stores, with 70.
	maxValoresFaceta = 1000
)

// errFiltroInvalido means Algolia refused the filters of the URL: the
// visitor's input, not a problem with CeX.
var errFiltroInvalido = errors.New("Algolia refused the filters")

// baseAlgolia is a variable only so tests can point it to a local server.
var baseAlgolia = "https://search.webuy.io"

// Only the fields that are rendered: about 700 B per result instead of the
// several KB the website gets with every store and highlight.
var camposBusqueda = []string{
	"boxId", "boxName", "categoryFriendlyName",
	"sellPrice", "cashPriceCalculated", "exchangePriceCalculated",
	"imageUrls", "inStockOnline", "inStockStore",
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
	// Whether it can be bought online and in a store. Not the quantities:
	// a product can have online stock and still not be sold online.
	// Measured on the whole catalogue, inStockOnline and inStockStore say
	// exactly what the availability field says, in one digit each.
	Online int `json:"inStockOnline"`
	Tienda int `json:"inStockStore"`
}

type resultadoBusqueda struct {
	Total     int        `json:"nbHits"`
	Paginas   int        `json:"nbPages"`
	Pagina    int        `json:"page"`
	Productos []producto `json:"hits"`
	// Every filter list Algolia offers for these results, with the number
	// of products for each value.
	Facetas map[string]map[string]int `json:"facets"`
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

// peticionBusqueda is everything that defines one page of results.
type peticionBusqueda struct {
	Consulta string
	Pagina   int    // from 0, as Algolia counts
	Orden    string // "" is relevance
	Filtros  filtros
}

// buscar returns one page of results.
func buscar(ctx context.Context, p peticionBusqueda) (resultadoBusqueda, error) {
	r, _, err := consultarAlgolia(ctx, p)
	return r, err
}

// normalizar makes "PS5", " ps5 " and "ps5" share one cache entry: Algolia
// ignores case and extra spaces anyway.
func normalizar(consulta string) string {
	return strings.ToLower(strings.Join(strings.Fields(consulta), " "))
}

// consultarAlgolia also returns the raw response, for the probe.
func consultarAlgolia(ctx context.Context, p peticionBusqueda) (resultadoBusqueda, respuesta, error) {
	p.Consulta = normalizar(p.Consulta)
	p.Orden = ordenValido(p.Orden)
	if p.Consulta == "" || p.Pagina < 0 {
		return resultadoBusqueda{}, respuesta{}, nil
	}
	cuerpo, err := json.Marshal(map[string]any{"requests": consultasAlgolia(p)})
	if err != nil {
		return resultadoBusqueda{}, respuesta{}, err
	}
	direccion := baseAlgolia + "/1/indexes/*/queries"

	// Each sort order and each set of filters is a different search with
	// its own cache entry. The filters are written in their canonical
	// order, so the same filters in another order share it.
	clave := p.Consulta + "|" + strconv.Itoa(p.Pagina) + "|" + p.Orden + "|" + p.Filtros.parametros()
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
	res, err := leerAlgolia(r, p.Filtros.Grupos)
	if err != nil {
		if r.Estado == http.StatusBadRequest && p.Filtros.activos() {
			// Odd filters typed into the URL, not a change at CeX: /salud
			// stays green.
			return resultadoBusqueda{}, r, errFiltroInvalido
		}
		// The answer arrived but is not what webye expects: Algolia or the
		// CeX proxy changed. /salud reports it.
		algolia.fallo(err)
	}
	return res, r, err
}

// consultasAlgolia builds the queries of one search, all sent in the same
// HTTP request, the way the website does.
func consultasAlgolia(p peticionBusqueda) []map[string]any {
	filtro := filtroBase
	if p.Filtros.Agotados {
		filtro = filtroVisible
	}
	numericos := p.Filtros.numericos()

	// No userToken and no click analytics: the website sends both for
	// Algolia's analytics, webye sends neither.
	principal := map[string]any{
		"indexName":             indicePara(p.Orden),
		"query":                 p.Consulta,
		"hitsPerPage":           resultadosPorPagina,
		"page":                  p.Pagina,
		"clickAnalytics":        false,
		"facets":                []string{"*"},
		"maxValuesPerFacet":     maxValoresFaceta,
		"attributesToRetrieve":  camposBusqueda,
		"attributesToHighlight": []string{},
		"filters":               filtro,
	}
	if f := p.Filtros.facetFilters(""); len(f) > 0 {
		principal["facetFilters"] = f
	}
	if len(numericos) > 0 {
		principal["numericFilters"] = numericos
	}
	consultas := []map[string]any{principal}

	// With a value ticked in a list, the main query only counts products
	// that have it, so the list would lose every other value. Like the
	// website, each list in use gets one more query that leaves its own
	// filter out, only to count its values: no products (hitsPerPage 0)
	// and no analytics. The website also sends one for the price, which is
	// a range and not a list, and never uses it; webye does not.
	for _, g := range p.Filtros.Grupos {
		recuento := map[string]any{
			"indexName":             indicePara(p.Orden),
			"query":                 p.Consulta,
			"hitsPerPage":           0,
			"page":                  0,
			"analytics":             false,
			"clickAnalytics":        false,
			"facets":                []string{g.Atributo},
			"maxValuesPerFacet":     maxValoresFaceta,
			"attributesToRetrieve":  []string{},
			"attributesToHighlight": []string{},
			"filters":               filtro,
		}
		if f := p.Filtros.facetFilters(g.Atributo); len(f) > 0 {
			recuento["facetFilters"] = f
		}
		if len(numericos) > 0 {
			recuento["numericFilters"] = numericos
		}
		consultas = append(consultas, recuento)
	}
	return consultas
}

// leerAlgolia decodes the answer: the results of the main query, with the
// counts of each list in use taken from its own query.
func leerAlgolia(r respuesta, grupos []grupoFiltro) (resultadoBusqueda, error) {
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
	if len(datos.Results) != 1+len(grupos) {
		return resultadoBusqueda{}, fmt.Errorf("Algolia answered %d queries out of %d", len(datos.Results), 1+len(grupos))
	}
	res := datos.Results[0]
	if res.Facetas == nil {
		res.Facetas = map[string]map[string]int{}
	}
	for i, g := range grupos {
		res.Facetas[g.Atributo] = datos.Results[i+1].Facetas[g.Atributo]
	}
	return res, nil
}
