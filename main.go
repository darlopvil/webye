package main

import (
	"embed"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	// Base de zonas horarias embebida: la imagen es scratch y no trae
	// /usr/share/zoneinfo, pero así la variable TZ funciona igual.
	_ "time/tzdata"
)

var (
	//go:embed templates
	plantillasFS embed.FS

	//go:embed static
	estaticosFS embed.FS
)

// entorno devuelve el valor de una variable de entorno, o el valor por
// defecto si no está definida o está vacía.
func entorno(nombre, porDefecto string) string {
	if v := os.Getenv(nombre); v != "" {
		return v
	}
	return porDefecto
}

func main() {
	// El host por defecto es 0.0.0.0 y no localhost: dentro de un contenedor,
	// localhost deja el proceso inalcanzable.
	puertoPorDefecto, err := strconv.Atoi(entorno("WEBYE_PORT", "8080"))
	if err != nil {
		log.Fatalf("WEBYE_PORT no es un número válido: %v", err)
	}
	puerto := flag.Int("p", puertoPorDefecto, "Puerto de escucha")
	host := flag.String("h", entorno("WEBYE_HOST", "0.0.0.0"), "Dirección de escucha")
	flag.Parse()

	mux := http.NewServeMux()

	// La portada nunca habla con CeX.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			renderizar(w, http.StatusNotFound, "error", datosPagina{
				Titulo:  "Página no encontrada",
				Mensaje: "Esta dirección no existe.",
			})
			return
		}
		renderizar(w, http.StatusOK, "inicio", datosPagina{Inicio: true})
	})

	// Los navegadores piden /favicon.ico sin mirar el <head>.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
	})

	mux.Handle("/static/", http.FileServer(http.FS(estaticosFS)))

	srv := &http.Server{
		Addr:              *host + ":" + strconv.Itoa(*puerto),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("Escuchando en %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
