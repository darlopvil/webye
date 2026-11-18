package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

var soloTiendaAlgolia = [][]string{{"availability:Disponible en tienda"}, {"availability:-Disponible online"}}

func TestTheExtraFiltersReachAlgolia(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4&storeOnly=1&discontinued=1&cashPriceCalculated=50:80&sellPrice=:100")
	c := (*recibidas)[0]
	// None of them is a list, so no count query.
	if len(c) != 1 {
		t.Fatalf("%d queries", len(c))
	}
	if !reflect.DeepEqual(c[0].FacetFilters, soloTiendaAlgolia) {
		t.Errorf("facetFilters %v", c[0].FacetFilters)
	}
	quiero := []string{"sellPrice<=100", "cashPriceCalculated>=50", "cashPriceCalculated<=80", "discontinued=1"}
	if !reflect.DeepEqual(c[0].NumericFilters, quiero) || c[0].Filters != filtroBase {
		t.Errorf("numericFilters %v, filters %q", c[0].NumericFilters, c[0].Filters)
	}
}

func TestStoreOnlyAppliesToTheCountQueriesToo(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4&storeOnly=1&Grado=A")
	c := (*recibidas)[0]
	if len(c) != 2 || !reflect.DeepEqual(c[1].FacetFilters, soloTiendaAlgolia) {
		t.Fatalf("count query filters %+v", c)
	}
	if !reflect.DeepEqual(c[0].FacetFilters, append([][]string{{"Grado:A"}}, soloTiendaAlgolia...)) {
		t.Errorf("main query filters %v", c[0].FacetFilters)
	}
}

func TestTheExtraFiltersComeFromTheForm(t *testing.T) {
	algoliaConFacetas(t, 1, nil, nil)
	w := pedirRuta(t, "/search/?stext=ps4&storeOnly=1&discontinued=1&precioMin=&precioMax=&efectivoMin=90&efectivoMax=20")
	if hacia := "/search/?stext=ps4&cashPriceCalculated=20:90&discontinued=1&storeOnly=1"; w.Code != http.StatusSeeOther || w.Header().Get("Location") != hacia {
		t.Fatalf("%d to %q, want %q", w.Code, w.Header().Get("Location"), hacia)
	}
}

func TestOnlyTheirOwnValuesTurnTheExtrasOn(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	pedirRuta(t, "/search/?stext=ps4&storeOnly=yes&discontinued=0&cashPriceCalculated=abc:")
	if c := (*recibidas)[0]; c[0].FacetFilters != nil || c[0].NumericFilters != nil {
		t.Fatalf("got %+v", c[0])
	}
}

func TestThePanelShowsTheExtras(t *testing.T) {
	algoliaConFacetas(t, 1, nil, nil)
	html := pedirRuta(t, "/search/?stext=ps4&storeOnly=1&discontinued=1&cashPriceCalculated=50:&sellPrice=10:").Body.String()
	for _, debe := range []string{
		`Filtros <span class="filtros-activos">4</span>`,
		`name="storeOnly" value="1" checked> Solo en tienda`,
		`name="discontinued" value="1" checked> Solo descatalogados`,
		`name="availability" value="allStock"> Mostrar productos agotados`,
		`<legend>Te dan en efectivo</legend>`,
		`name="efectivoMin" value="50"`,
		`name="efectivoMax" value=""`,
		`name="precioMin" value="10"`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
}
