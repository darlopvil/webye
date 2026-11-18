package main

import (
	"bytes"
	"html/template"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// datosPagina is what every template receives. Each new page adds its own
// fields here.
type datosPagina struct {
	Titulo   string
	Mensaje  string
	Consulta string
	// Inicio hides the header search box on the home page, which has its own
	// big one.
	Inicio   bool
	Busqueda *vistaBusqueda
	Ficha    *vistaFicha
}

var funciones = template.FuncMap{
	"euros":    euros,
	"miles":    miles,
	"deref":    func(v *float64) float64 { return *v },
	"decimal":  decimal,
	"foto":     foto,
	"estatico": estatico,
}

// Each page is parsed into its own set together with the base template,
// because every page defines the same "contenido" block.
func cargarPlantilla(pagina string) *template.Template {
	return template.Must(template.New("base.html").Funcs(funciones).ParseFS(
		plantillasFS,
		"templates/base.html",
		"templates/"+pagina+".html",
	))
}

var plantillas = map[string]*template.Template{
	"inicio":     cargarPlantilla("inicio"),
	"resultados": cargarPlantilla("resultados"),
	"ficha":      cargarPlantilla("ficha"),
	"about":      cargarPlantilla("about"),
	"error":      cargarPlantilla("error"),
}

// renderizar executes the template into a buffer before writing anything, so
// a failure halfway through the page becomes a clean 500 instead of half a
// page served with a 200.
func renderizar(w http.ResponseWriter, estado int, pagina string, datos datosPagina) {
	var buf bytes.Buffer
	if err := plantillas[pagina].ExecuteTemplate(&buf, "base.html", datos); err != nil {
		log.Printf("error rendering %s: %v", pagina, err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(estado)
	_, _ = buf.WriteTo(w)
}

// miles writes an integer the Spanish way: 1182 → "1.182".
func miles(n int) string {
	s := strconv.Itoa(n)
	signo := ""
	if strings.HasPrefix(s, "-") {
		signo, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return signo + s
}

// euros writes a price the Spanish way, with cents only when there are any:
// 1100 → "1.100 €", 15.5 → "15,50 €".
func euros(v float64) string {
	centimos := int(math.Round(v * 100))
	if centimos%100 == 0 {
		return miles(centimos/100) + " €"
	}
	return miles(centimos/100) + "," + strconv.Itoa(abs(centimos%100)/10) + strconv.Itoa(abs(centimos%10)) + " €"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// decimal writes a number with one decimal the Spanish way: 4.8 → "4,8".
func decimal(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}
