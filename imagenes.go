package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Product photos are served by webye, so the browser never contacts CeX.
// Only CeX's own product image folder can be reached: /img/<path> maps to
// es.static.webuy.com/product_images/<path> and nothing else.

// baseImagenes is a variable only so tests can point it to a local server.
var baseImagenes = "https://es.static.webuy.com"

const carpetaImagenes = "/product_images/"

// A results page shows up to resultadosPorPagina photos. The image limiter
// lets two full pages load at once and then one page of photos per minute.
var limiteImagenes = nuevoLimitador(2*resultadosPorPagina, resultadosPorPagina/60.0)

var extensionesImagen = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// foto turns a CeX image URL into its local /img/ path, or "" if the URL is
// not in CeX's product image folder. Paths carry spaces and accents
// ("Móviles/Moviles - iPhone/…"), so every segment is escaped.
func foto(origen string) string {
	u, err := url.Parse(origen)
	if err != nil || u.Scheme != "https" || u.Host != strings.TrimPrefix(baseImagenes, "https://") ||
		!strings.HasPrefix(u.Path, carpetaImagenes) {
		return ""
	}
	segmentos, ok := segmentosValidos(strings.TrimPrefix(u.Path, carpetaImagenes))
	if !ok {
		return ""
	}
	return "/img/" + escaparRuta(segmentos)
}

// segmentosValidos splits an image path and rejects anything that is not a
// plain path to an image file.
func segmentosValidos(ruta string) ([]string, bool) {
	if ruta == "" || len(ruta) > 512 {
		return nil, false
	}
	segmentos := strings.Split(ruta, "/")
	for _, s := range segmentos {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "\\\x00") {
			return nil, false
		}
	}
	if !extensionesImagen[strings.ToLower(path.Ext(segmentos[len(segmentos)-1]))] {
		return nil, false
	}
	return segmentos, true
}

func escaparRuta(segmentos []string) string {
	e := make([]string, len(segmentos))
	for i, s := range segmentos {
		e[i] = url.PathEscape(s)
	}
	return strings.Join(e, "/")
}

// imagen serves /img/<path> through the image guardian, which keeps each
// photo for as long as CeX's own max-age allows.
func imagen(w http.ResponseWriter, r *http.Request) {
	segmentos, ok := segmentosValidos(strings.TrimPrefix(r.URL.Path, "/img/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ruta := escaparRuta(segmentos)
	res, err := imagenes.obtener(r.Context(), ruta, 0, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, baseImagenes+carpetaImagenes+ruta, nil)
	})
	if err != nil {
		// No path in the log: it would say what was being looked at.
		log.Printf("image failed: %v", err)
	}
	switch {
	case errors.Is(err, errPausado), errors.Is(err, errBloqueo), errors.Is(err, errContenido):
		http.Error(w, "CeX no responde", http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, "No se ha podido traer la imagen", http.StatusBadGateway)
		return
	case res.Estado >= 300 && res.Estado < 400:
		// CeX has no photo for this product and redirects to its generic
		// picture. webye shows its own placeholder instead of following.
		cachearFoto(w, res.Caduca, imagenes.ahora())
		sinFoto(w)
		return
	case res.Estado != http.StatusOK:
		http.NotFound(w, r)
		return
	}
	cachearFoto(w, res.Caduca, imagenes.ahora())
	w.Header().Set("Content-Type", res.Tipo)
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Cuerpo)))
	_, _ = w.Write(res.Cuerpo)
}

// sinFoto serves webye's own "no photo" placeholder.
func sinFoto(w http.ResponseWriter) {
	b, err := estaticosFS.ReadFile("static/sin-foto.svg")
	if err != nil {
		http.Error(w, "Sin foto", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
