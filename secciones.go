package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Browsing by section, with the CeX website's own levels and URLs:
//
//	supercategory  /supercat?superCatId=1&superCatName=Juegos
//	product line   /search?productLineId=82&productLineName=PlayStation 4
//	category       /search?categoryIds=1001&categoryName=PS4 Juegos
//
// The lists come from the wss2 calls the website makes, kept as long as its
// service worker keeps them: supercategories and product lines 24 h,
// categories 30 min. A product line or a category is a search without
// text: Algolia filters a product line by the number productLineId (it is
// not a facet) and a category by the facet categoryId, both measured to
// give the same results as filtering by their names.

const (
	ttlSecciones  = 24 * time.Hour
	ttlCategorias = 30 * time.Minute

	// After a failed attempt to get the sections, wait as long as the
	// guardian's first pause before trying again.
	esperaSecciones = pausaInicial

	// Names come from the URL, as on the website, and are only shown as
	// titles: escaped, and cut at a length no section name reaches.
	largoNombre = 80
)

type superCat struct {
	ID     int    `json:"superCatId"`
	Nombre string `json:"superCatFriendlyName"`
}

type lineaProducto struct {
	ID     int    `json:"productLineId"`
	Nombre string `json:"productLineName"`
}

type categoriaCeX struct {
	ID     int    `json:"categoryId"`
	Nombre string `json:"categoryFriendlyName"`
}

// errSinSeccion means CeX does not know that section.
var errSinSeccion = errors.New("unknown section")

// pedirLista asks wss2 for one of its lists: response.data.<campo>.
func pedirLista[T any](ctx context.Context, clave, ruta, campo string, ttl time.Duration) ([]T, error) {
	r, err := wss2.obtener(ctx, clave, ttl, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, baseWss2+ruta, nil)
	})
	if err != nil {
		return nil, err
	}
	// An unknown ID is a 400 ("Invalid SuperCatId").
	if r.Estado >= 400 && r.Estado < 500 {
		return nil, errSinSeccion
	}
	if r.Estado != http.StatusOK {
		err := fmt.Errorf("wss2 answered %d", r.Estado)
		wss2.fallo(err)
		return nil, err
	}
	var datos struct {
		Response struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"response"`
	}
	var lista []T
	if err := json.Unmarshal(r.Cuerpo, &datos); err != nil {
		err = fmt.Errorf("unreadable wss2 %s response: %w", campo, err)
		wss2.fallo(err)
		return nil, err
	}
	if err := json.Unmarshal(datos.Response.Data[campo], &lista); err != nil {
		err = fmt.Errorf("wss2 response without %s: %w", campo, err)
		wss2.fallo(err)
		return nil, err
	}
	return lista, nil
}

func obtenerSupercats(ctx context.Context) ([]superCat, error) {
	return pedirLista[superCat](ctx, "supercats", "/v3/supercats", "superCats", ttlSecciones)
}

func obtenerLineas(ctx context.Context, superCat int) ([]lineaProducto, error) {
	id := strconv.Itoa(superCat)
	return pedirLista[lineaProducto](ctx, "productlines|"+id, "/v3/productlines?superCatId="+id, "productLines", ttlSecciones)
}

// obtenerCategorias wants the product line as a list, as the website sends
// it: productLineIds=[82].
func obtenerCategorias(ctx context.Context, linea int) ([]categoriaCeX, error) {
	id := strconv.Itoa(linea)
	return pedirLista[categoriaCeX](ctx, "categories|"+id, "/v3/categories?productLineIds=%5B"+id+"%5D", "categories", ttlCategorias)
}

// ---------- the sections bar in the header ----------

// secciones holds the supercategories for every page. A page never waits
// for CeX to draw them: mantenerSecciones fills this in the background, and
// without it the bar is simply not drawn.
var secciones atomic.Pointer[[]superCat]

func seccionesActuales() []superCat {
	if s := secciones.Load(); s != nil {
		return *s
	}
	return nil
}

// refrescarSecciones asks for the sections once and says when to ask
// again: when they expire, or soon after a failure. The last good list is
// kept meanwhile.
func refrescarSecciones(ctx context.Context) time.Duration {
	s, err := obtenerSupercats(ctx)
	if err != nil || len(s) == 0 {
		log.Printf("sections for the header: %v", err)
		return esperaSecciones
	}
	secciones.Store(&s)
	return ttlSecciones
}

// mantenerSecciones runs for the whole life of the server: one request to
// CeX at start-up and then one a day.
func mantenerSecciones(ctx context.Context) {
	for {
		espera := refrescarSecciones(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(espera):
		}
	}
}

// ---------- the supercategory page ----------

type enlaceSeccion struct {
	Nombre string
	URL    string
}

type vistaSeccion struct {
	Nombre string
	Lineas []enlaceSeccion
}

// paginaSuperCat serves /supercat?superCatId=…, the product lines of a
// supercategory, each linking to its search, as on the website.
func paginaSuperCat(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, ok := leerID(q.Get("superCatId"))
	var lineas []lineaProducto
	var err error
	if ok {
		lineas, err = obtenerLineas(r.Context(), id)
	}
	if !ok || errors.Is(err, errSinSeccion) {
		renderizar(w, http.StatusNotFound, "error", datosPagina{
			Titulo:  "Sección no encontrada",
			Mensaje: "CeX no tiene esa sección.",
		})
		return
	}
	if err != nil {
		errorCeX(w, "", err)
		return
	}

	nombre := nombreDeURL(q.Get("superCatName"))
	for _, s := range seccionesActuales() {
		if nombre == "" && s.ID == id {
			nombre = s.Nombre
		}
	}
	if nombre == "" {
		nombre = "Sección " + strconv.Itoa(id)
	}
	v := &vistaSeccion{Nombre: nombre}
	for _, l := range lineas {
		v.Lineas = append(v.Lineas, enlaceSeccion{Nombre: l.Nombre, URL: enlaceBusqueda("", ambito{Linea: l.ID, LineaNombre: l.Nombre}, "", filtros{}, 1)})
	}
	renderizar(w, http.StatusOK, "seccion", datosPagina{Titulo: nombre, Seccion: v})
}

// ---------- product lines and categories in a search ----------

// ambito is the part of the catalogue a search is limited to.
type ambito struct {
	Linea           int
	LineaNombre     string
	Categorias      []int
	CategoriaNombre string
}

var reID = regexp.MustCompile(`^[0-9]{1,9}$`)

func leerID(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if !reID.MatchString(s) {
		return 0, false
	}
	id, err := strconv.Atoi(s)
	return id, err == nil && id > 0
}

func nombreDeURL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for utf8.RuneCountInString(s) > largoNombre {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}

// leerAmbito reads productLineId and categoryIds (one or more, separated by
// commas), ignoring anything that is not an ID.
func leerAmbito(q url.Values) ambito {
	var a ambito
	if id, ok := leerID(q.Get("productLineId")); ok {
		a.Linea, a.LineaNombre = id, nombreDeURL(q.Get("productLineName"))
	}
	for _, s := range strings.Split(q.Get("categoryIds"), ",") {
		if id, ok := leerID(s); ok && !slices.Contains(a.Categorias, id) && len(a.Categorias) < maxValoresFaceta {
			a.Categorias = append(a.Categorias, id)
		}
	}
	slices.Sort(a.Categorias)
	if len(a.Categorias) > 0 {
		a.CategoriaNombre = nombreDeURL(q.Get("categoryName"))
	}
	return a
}

func (a ambito) usado() bool { return a.Linea > 0 || len(a.Categorias) > 0 }

func (a ambito) nombre() string {
	switch {
	case a.CategoriaNombre != "":
		return a.CategoriaNombre
	case len(a.Categorias) > 0:
		return "Categoría " + a.idsCategorias()
	case a.LineaNombre != "":
		return a.LineaNombre
	case a.Linea > 0:
		return "Línea " + strconv.Itoa(a.Linea)
	}
	return ""
}

func (a ambito) idsCategorias() string {
	ids := make([]string, len(a.Categorias))
	for i, c := range a.Categorias {
		ids[i] = strconv.Itoa(c)
	}
	return strings.Join(ids, ",")
}

// campos are the URL parameters of the ambit, in the website's order.
func (a ambito) campos() [][2]string {
	var c [][2]string
	if a.Linea > 0 {
		c = append(c, [2]string{"productLineId", strconv.Itoa(a.Linea)})
		if a.LineaNombre != "" {
			c = append(c, [2]string{"productLineName", a.LineaNombre})
		}
	}
	if len(a.Categorias) > 0 {
		c = append(c, [2]string{"categoryIds", a.idsCategorias()})
		if a.CategoriaNombre != "" {
			c = append(c, [2]string{"categoryName", a.CategoriaNombre})
		}
	}
	return c
}

func (a ambito) parametros() []string {
	var p []string
	for _, c := range a.campos() {
		v := url.QueryEscape(c[1])
		if c[0] == "categoryIds" {
			v = c[1] // digits and commas, as the website writes them
		}
		p = append(p, c[0]+"="+v)
	}
	return p
}

// categoriasAlgolia limits a search to its categories: one group, so any
// of them.
func (a ambito) categoriasAlgolia() [][]string {
	if len(a.Categorias) == 0 {
		return nil
	}
	grupo := make([]string, len(a.Categorias))
	for i, c := range a.Categorias {
		grupo[i] = "categoryId:" + strconv.Itoa(c)
	}
	return [][]string{grupo}
}
