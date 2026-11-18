package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
)

// Product details come from CeX's wss2 API, the same one its website uses
// for product pages.

// baseWss2 is a variable only so tests can point it to a local server.
var baseWss2 = "https://wss2.cex.es.webuy.io"

// Box IDs are letters and digits only, the same pattern CeX's service
// worker uses for this route (/v3/boxes/([a-zA-Z0-9]+)/detail).
var reIDCaja = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)

var errSinProducto = errors.New("product not found")

type atributo struct {
	Nombre  string `json:"attributeFriendlyName"`
	Valores []any  `json:"attributeValue"`
	Mostrar int    `json:"showOnPDP"`
}

type ficha struct {
	ID             string   `json:"boxId"`
	Nombre         string   `json:"boxName"`
	Categoria      string   `json:"categoryFriendlyName"`
	Supercategoria string   `json:"superCatFriendlyName"`
	Venta          float64  `json:"sellPrice"`
	Efectivo       float64  `json:"cashPrice"`
	Vale           float64  `json:"exchangePrice"`
	PrecioSalida   *float64 `json:"firstPrice"`
	PrecioAnterior *float64 `json:"previousPrice"`
	CambioPrecio   string   `json:"lastPriceUpdatedDate"`
	Valoracion     *float64 `json:"boxRating"`
	Online         int      `json:"inStockOnline"` // not ecomQuantityOnHand, see busqueda.go
	Tienda         int      `json:"collectionQuantity"`
	Imagenes       struct {
		Pequena string `json:"small"`
		Mediana string `json:"medium"`
		Grande  string `json:"large"`
	} `json:"imageUrls"`
	Descripcion string     `json:"boxDescription"`
	Atributos   []atributo `json:"attributeInfo"`
}

// especificacion is one row of the specifications table.
type especificacion struct {
	Nombre string
	Valor  string
}

type vistaFicha struct {
	ficha
	Especificaciones []especificacion
	DescripcionHTML  template.HTML
	FechaCambio      string // dd/mm/yyyy
	HuboOtroPrecio   bool
	Tiendas          *vistaTiendas // only when the visitor asked for them
}

// obtenerFicha returns the details of one product, from the cache when
// possible.
func obtenerFicha(ctx context.Context, id string) (ficha, error) {
	if !reIDCaja.MatchString(id) {
		return ficha{}, errSinProducto
	}
	r, err := wss2.obtener(ctx, "detail|"+id, ttlFicha, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, baseWss2+"/v3/boxes/"+id+"/detail", nil)
	})
	if err != nil {
		return ficha{}, err
	}
	if r.Estado >= 400 && r.Estado < 500 {
		return ficha{}, errSinProducto
	}
	if r.Estado != http.StatusOK {
		err := fmt.Errorf("wss2 answered %d", r.Estado)
		wss2.fallo(err)
		return ficha{}, err
	}
	var datos struct {
		Response struct {
			Ack  string `json:"ack"`
			Data struct {
				BoxDetails []ficha `json:"boxDetails"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(r.Cuerpo, &datos); err != nil {
		// The answer arrived but no longer has the expected shape: the wss2
		// API changed. /salud reports it.
		err = fmt.Errorf("unreadable wss2 response: %w", err)
		wss2.fallo(err)
		return ficha{}, err
	}
	if datos.Response.Ack != "Success" || len(datos.Response.Data.BoxDetails) == 0 {
		return ficha{}, errSinProducto
	}
	return datos.Response.Data.BoxDetails[0], nil
}

// paginaFicha serves /product-detail/?id=…, the same URL as CeX. The other
// parameters CeX adds (categoryName, queryID, position…) are ignored.
func paginaFicha(w http.ResponseWriter, r *http.Request) {
	f, err := obtenerFicha(r.Context(), strings.TrimSpace(r.URL.Query().Get("id")))
	if errors.Is(err, errSinProducto) {
		renderizar(w, http.StatusNotFound, "error", datosPagina{
			Titulo:  "Producto no encontrado",
			Mensaje: "CeX no tiene ningún producto con ese código.",
		})
		return
	}
	if err != nil {
		errorCeX(w, "", err)
		return
	}

	v := &vistaFicha{ficha: f, DescripcionHTML: sanear(f.Descripcion)}
	for _, a := range f.Atributos {
		if a.Mostrar != 1 || len(a.Valores) == 0 {
			continue
		}
		valores := make([]string, len(a.Valores))
		for i, x := range a.Valores {
			valores[i] = fmt.Sprint(x)
		}
		v.Especificaciones = append(v.Especificaciones, especificacion{a.Nombre, strings.Join(valores, ", ")})
	}
	// "2026-10-20 01:00:06" → "20/10/2026"
	if len(f.CambioPrecio) >= 10 {
		p := f.CambioPrecio
		v.FechaCambio = p[8:10] + "/" + p[5:7] + "/" + p[0:4]
	}
	v.HuboOtroPrecio = f.PrecioAnterior != nil && *f.PrecioAnterior != f.Venta && *f.PrecioAnterior > 0

	// Stock per store only when asked for (&stores=1), as on the website:
	// one more request to CeX, never on a plain product page.
	if r.URL.Query().Get("stores") == "1" {
		v.Tiendas = vistaDeTiendas(obtenerTiendas(r.Context(), f.ID))
	}

	renderizar(w, http.StatusOK, "ficha", datosPagina{Titulo: f.Nombre, Ficha: v})
}
