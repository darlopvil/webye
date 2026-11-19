package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	Titulo     string          // the product line or category, if any
	Donde      string          // «what» was searched, and where
	Categorias []enlaceSeccion // a product line's categories
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
	amb := leerAmbito(q)
	if consulta == "" && !amb.usado() {
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
		http.Redirect(w, r, enlaceBusqueda(consulta, amb, orden, f, 1), http.StatusSeeOther)
		return
	}

	res, err := buscar(r.Context(), peticionBusqueda{Consulta: consulta, Pagina: pagina - 1, Orden: orden, Filtros: f, Ambito: amb})
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
		Titulo:     amb.nombre(),
	}
	titulo := consulta
	switch {
	case consulta != "" && amb.usado():
		v.Donde = "para «" + consulta + "» en «" + amb.nombre() + "»"
		titulo = consulta + " · " + amb.nombre()
	case amb.usado():
		v.Donde = "en «" + amb.nombre() + "»"
		titulo = amb.nombre()
	default:
		v.Donde = "para «" + consulta + "»"
	}
	// A product line links to its categories, as on the website. Without
	// them the page still works, so a failure only leaves them out.
	if amb.Linea > 0 && len(amb.Categorias) == 0 {
		if cats, err := obtenerCategorias(r.Context(), amb.Linea); err == nil {
			for _, c := range cats {
				v.Categorias = append(v.Categorias, enlaceSeccion{Nombre: c.Nombre, URL: enlaceBusqueda("", ambito{Categorias: []int{c.ID}, CategoriaNombre: c.Nombre}, "", filtros{}, 1)})
			}
		}
	}
	// With no results and no filters there is nothing to narrow down.
	if !v.SinResults || v.ConFiltros {
		v.Filtros = vistaDeFiltros(consulta, amb, orden, f, res.Facetas)
	}
	// Changing the order keeps the filters and goes back to the first page.
	for _, o := range opcionesOrden {
		v.Ordenes = append(v.Ordenes, enlaceOrden{Etiqueta: o.Etiqueta, URL: enlaceBusqueda(consulta, amb, o.Valor, f, 1), Actual: o.Valor == orden})
	}
	// The pages keep the order and the filters.
	enlace := func(n int) string { return enlaceBusqueda(consulta, amb, orden, f, n) }
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
		Titulo:   titulo,
		Consulta: consulta,
		Busqueda: v,
	})
}

// enlaceBusqueda builds a results link with the shape of CeX's own
// (/search/?stext=tomb+raider&categoryIds=1001&stores=…&page=2), plus sort
// when it is not relevance.
func enlaceBusqueda(consulta string, a ambito, orden string, f filtros, pagina int) string {
	var partes []string
	if consulta != "" {
		partes = append(partes, "stext="+url.QueryEscape(consulta))
	}
	partes = append(partes, a.parametros()...)
	if orden != "" {
		partes = append(partes, "sort="+url.QueryEscape(orden))
	}
	if p := strings.TrimPrefix(f.parametros(), "&"); p != "" {
		partes = append(partes, p)
	}
	if pagina > 1 {
		partes = append(partes, "page="+strconv.Itoa(pagina))
	}
	return "/search/?" + strings.Join(partes, "&")
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
