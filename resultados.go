package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
)

// Algolia only lets the first 1,000 results of a search be browsed
// (measured: with one result per page, nbPages is exactly 1000).
const limiteAlgolia = 1000

type vistaBusqueda struct {
	Total      int
	Recortado  bool // more results than Algolia lets anyone browse
	Productos  []producto
	Pagina     int // from 1, as in CeX's own URLs (?page=2)
	Paginas    []enlacePagina
	Anterior   string
	Siguiente  string
	Ordenes    []enlaceOrden
	Filtros    *vistaFiltros
	ConFiltros bool
	SinResults bool
}

type enlaceOrden struct {
	Etiqueta string
	URL      string
	Actual   bool
}

type enlacePagina struct {
	Numero int
	URL    string
	Actual bool
}

// paginaBusqueda serves /search/?stext=…&page=N with the website's own
// filter parameters (see filtros.go), plus sort=…, which CeX keeps out of
// its URLs. Without it the order is relevance, so a CeX link still gives
// what CeX gives.
func paginaBusqueda(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	consulta := normalizar(q.Get("stext"))
	if consulta == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	pagina, err := strconv.Atoi(q.Get("page"))
	if err != nil || pagina < 1 {
		pagina = 1
	}
	orden := ordenValido(q.Get("sort"))
	f := leerFiltros(q)

	// The filter form sends repeated parameters and two price fields.
	// Its result goes to the website's URL shape, which is the one to
	// keep or share, without asking CeX anything.
	if desdeFormulario(q) {
		http.Redirect(w, r, enlaceBusqueda(consulta, orden, f, 1), http.StatusSeeOther)
		return
	}

	res, err := buscar(r.Context(), peticionBusqueda{Consulta: consulta, Pagina: pagina - 1, Orden: orden, Filtros: f})
	if errors.Is(err, errFiltroInvalido) {
		renderizar(w, http.StatusBadRequest, "error", datosPagina{
			Titulo:   "Filtros no válidos",
			Consulta: consulta,
			Mensaje:  "CeX no entiende esos filtros. Quítalos y vuelve a probar.",
		})
		return
	}
	if err != nil {
		errorCeX(w, consulta, err)
		return
	}

	v := &vistaBusqueda{
		Total:      res.Total,
		Recortado:  res.Total > limiteAlgolia,
		Productos:  res.Productos,
		Pagina:     pagina,
		ConFiltros: f.activos(),
		SinResults: len(res.Productos) == 0,
	}
	// With no results and no filters there is nothing to narrow down.
	if !v.SinResults || v.ConFiltros {
		v.Filtros = vistaDeFiltros(consulta, orden, f, res.Facetas)
	}
	// Changing the order keeps the filters and goes back to the first page.
	for _, o := range opcionesOrden {
		v.Ordenes = append(v.Ordenes, enlaceOrden{Etiqueta: o.Etiqueta, URL: enlaceBusqueda(consulta, o.Valor, f, 1), Actual: o.Valor == orden})
	}
	// The pages keep the order and the filters.
	enlace := func(n int) string { return enlaceBusqueda(consulta, orden, f, n) }
	if res.Paginas > 1 {
		for n := 1; n <= res.Paginas; n++ {
			v.Paginas = append(v.Paginas, enlacePagina{Numero: n, URL: enlace(n), Actual: n == pagina})
		}
		if pagina > 1 && pagina <= res.Paginas {
			v.Anterior = enlace(pagina - 1)
		}
		if pagina < res.Paginas {
			v.Siguiente = enlace(pagina + 1)
		}
	}
	renderizar(w, http.StatusOK, "resultados", datosPagina{
		Titulo:   consulta,
		Consulta: consulta,
		Busqueda: v,
	})
}

// enlaceBusqueda builds a results link with the shape of CeX's own
// (/search/?stext=tomb+raider&stores=…&page=2), plus sort when it is not
// relevance.
func enlaceBusqueda(consulta, orden string, f filtros, pagina int) string {
	u := "/search/?stext=" + url.QueryEscape(consulta)
	if orden != "" {
		u += "&sort=" + url.QueryEscape(orden)
	}
	u += f.parametros()
	if pagina > 1 {
		u += "&page=" + strconv.Itoa(pagina)
	}
	return u
}

// errorCeX explains what went wrong when CeX could not be reached, without
// a 500 and without retrying.
func errorCeX(w http.ResponseWriter, consulta string, err error) {
	datos := datosPagina{Titulo: "CeX no responde", Consulta: consulta}
	switch {
	case errors.Is(err, errPausado), errors.Is(err, errBloqueo), errors.Is(err, errContenido):
		datos.Mensaje = "CeX nos ha pedido un respiro. webye deja de llamar durante un rato; vuelve a intentarlo en unos minutos."
	default:
		datos.Mensaje = "No se ha podido hablar con CeX. Vuelve a intentarlo en un momento."
	}
	log.Printf("search failed: %v", err)
	renderizar(w, http.StatusServiceUnavailable, "error", datos)
}
