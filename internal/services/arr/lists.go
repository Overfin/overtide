package arr

import (
	"fmt"
	"net/http"
)

// ImportLists returns the app's import lists whole, each as the app gives
// it, so one can be edited and saved back (SaveImportList) without losing
// the fields Overtide doesn't know.
func (client *Client) ImportLists() ([]map[string]any, error) {
	var lists []map[string]any
	return lists, client.Get("/api/v3/importlist", &lists)
}

// SaveImportList saves an import list read with ImportLists.
func (client *Client) SaveImportList(list map[string]any) error {
	id, ok := list["id"].(float64)
	if !ok {
		return fmt.Errorf("%s import list without an id", client.app)
	}
	return client.Send(http.MethodPut, fmt.Sprintf("/api/v3/importlist/%d", int(id)), list, nil)
}
