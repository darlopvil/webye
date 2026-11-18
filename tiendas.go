package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Stock per store comes from the same wss2 call the CeX website makes when
// "Recoge hoy, consulta stock en tienda" is pressed on a product page:
// /v3/boxes/{id}/neareststores, with latitude and longitude 0, as the
// website sends without the visitor's location. The website does not ask
// for it when the page opens, so neither does webye: only when the visitor
// asks to see the stores.

// The same lifetime CeX allows its own stock data in its service worker
// (5 minutes for /v3/boxes/{id}/stock, "high-churn stock data").
const ttlTiendas = 5 * time.Minute

var madrid = cargarMadrid()

func cargarMadrid() *time.Location {
	l, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		panic(err) // time/tzdata is embedded: only a broken build gets here
	}
	return l
}

// ahoraTiendas is a variable only so tests can fix the clock.
var ahoraTiendas = time.Now

type horarioCeX struct {
	Abre   map[string]string `json:"open"`
	Cierra map[string]string `json:"close"`
}

type tiendaCeX struct {
	Nombre   string     `json:"storeName"`
	Unidades any        `json:"quantityOnHand"` // "1" to "4", or "4+": CeX stops counting at four
	Recogida int        `json:"isAvailableForCollection"`
	Horario  horarioCeX `json:"timings"`
	// Never filled in when probed, so their shape is unknown: a store with
	// either of them shows no opening hours rather than wrong ones.
	Festivos      json.RawMessage `json:"holidayHours"`
	HorasFestivos json.RawMessage `json:"holidayTimings"`
}

type tiendaVista struct {
	Nombre   string
	Unidades string
	Recogida bool
	Horario  string
}

type vistaTiendas struct {
	Lista []tiendaVista
	Error string
}

func obtenerTiendas(ctx context.Context, id string) ([]tiendaCeX, error) {
	if !reIDCaja.MatchString(id) {
		return nil, errSinProducto
	}
	r, err := wss2.obtener(ctx, "stores|"+id, ttlTiendas, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, baseWss2+"/v3/boxes/"+id+"/neareststores?latitude=0&longitude=0", nil)
	})
	if err != nil {
		return nil, err
	}
	if r.Estado >= 400 && r.Estado < 500 {
		return nil, errSinProducto
	}
	if r.Estado != http.StatusOK {
		err := fmt.Errorf("wss2 answered %d", r.Estado)
		wss2.fallo(err)
		return nil, err
	}
	var datos struct {
		Response struct {
			Data *struct {
				Tiendas []tiendaCeX `json:"nearestStores"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(r.Cuerpo, &datos); err != nil {
		err = fmt.Errorf("unreadable wss2 stores response: %w", err)
		wss2.fallo(err)
		return nil, err
	}
	// Only stores with stock come back; none at all means no store has it.
	if datos.Response.Data == nil {
		return nil, nil
	}
	return datos.Response.Data.Tiendas, nil
}

// vistaDeTiendas turns CeX's answer into the panel, alphabetically, as the
// website lists them.
func vistaDeTiendas(tiendas []tiendaCeX, err error) *vistaTiendas {
	v := &vistaTiendas{}
	if err != nil {
		v.Error = "No se ha podido consultar el stock en tiendas. Vuelve a intentarlo en un momento."
		return v
	}
	ahora := ahoraTiendas().In(madrid)
	for _, t := range tiendas {
		v.Lista = append(v.Lista, tiendaVista{
			Nombre:   t.Nombre,
			Unidades: strings.TrimSpace(fmt.Sprint(t.Unidades)),
			Recogida: t.Recogida == 1,
			Horario:  horarioHoy(t, ahora),
		})
	}
	slices.SortFunc(v.Lista, func(a, b tiendaVista) int { return ordenNatural(a.Nombre, b.Nombre) })
	return v
}

// horarioHoy says whether a store is open now, in its own words, or "" if
// its hours are not clear.
func horarioHoy(t tiendaCeX, ahora time.Time) string {
	if hayDato(t.Festivos) || hayDato(t.HorasFestivos) {
		return ""
	}
	dia := strings.ToLower(ahora.Weekday().String())
	abre, cierra := t.Horario.Abre[dia], t.Horario.Cierra[dia]
	if abre == "closed" || cierra == "closed" {
		return "Cerrado hoy"
	}
	if !esHora(abre) || !esHora(cierra) {
		return ""
	}
	// "HH:MM" strings compare in time order.
	switch hora := ahora.Format("15:04"); {
	case hora < abre:
		return "Abre hoy a las " + abre
	case hora < cierra:
		return "Abierto hasta las " + cierra
	default:
		return "Cerrado ahora"
	}
}

func hayDato(b json.RawMessage) bool {
	s := strings.TrimSpace(string(b))
	return s != "" && s != "null" && s != "[]" && s != "{}" && s != `""`
}

func esHora(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil && len(s) == 5
}

// paginaTiendas serves /product-detail/stores?id=…: the panel's list alone,
// for the script that opens it without reloading the page.
func paginaTiendas(w http.ResponseWriter, r *http.Request) {
	tiendas, err := obtenerTiendas(r.Context(), strings.TrimSpace(r.URL.Query().Get("id")))
	estado := http.StatusOK
	switch {
	case errors.Is(err, errSinProducto):
		estado = http.StatusNotFound
	case err != nil:
		estado = http.StatusServiceUnavailable
	}
	renderizarFragmento(w, estado, "tiendas", vistaDeTiendas(tiendas, err))
}
