package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"
)

// sondear runs one real search through the guardian, exactly as the search
// page will, prints a summary and returns the exit code. It is the way to
// check from the server that Go gets the same answer as curl.
func sondear(consulta string) int {
	inicio := time.Now()
	res, crudo, err := consultarAlgolia(context.Background(), peticionBusqueda{Consulta: consulta})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("status %d | %s | %d B | %s\n", crudo.Estado, crudo.Tipo, len(crudo.Cuerpo), time.Since(inicio).Round(time.Millisecond))
	fmt.Printf("nbHits %d | nbPages %d | page %d | hits %d\n", res.Total, res.Paginas, res.Pagina, len(res.Productos))
	// params is what Algolia finally applied, including anything the CeX
	// proxy adds on its way.
	var extra struct {
		Results []struct {
			Params string `json:"params"`
		} `json:"results"`
	}
	if json.Unmarshal(crudo.Cuerpo, &extra) == nil && len(extra.Results) > 0 {
		if p, err := url.QueryUnescape(extra.Results[0].Params); err == nil {
			fmt.Printf("params: %s\n", p)
		}
	}
	if len(res.Productos) > 0 {
		primero, _ := json.Marshal(res.Productos[0])
		fmt.Printf("first: %s\n", primero)
	}
	return 0
}
