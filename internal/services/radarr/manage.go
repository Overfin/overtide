package radarr

import (
	"fmt"
	"net/http"
	"net/url"
)

// EditTags adds the tag to the films, or takes it off them, in one call.
func (client *Client) EditTags(ids []int, tagID int, add bool) error {
	if len(ids) == 0 {
		return nil
	}
	apply := "remove"
	if add {
		apply = "add"
	}
	return client.Send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "tags": []int{tagID}, "applyTags": apply}, nil)
}

// SetManyMonitored monitors the films, or stops, in one call.
func (client *Client) SetManyMonitored(ids []int, monitored bool) error {
	if len(ids) == 0 {
		return nil
	}
	return client.Send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "monitored": monitored}, nil)
}

// cutoffPages bounds how many pages of the cutoff list are read (1000 each).
const cutoffPages = 200

// BelowCutoff returns the IDs of the unmonitored films below their quality
// profile's cutoff. The list compares qualities only, not custom format
// scores.
func (client *Client) BelowCutoff() ([]int, error) {
	var out []int
	for page := 1; page <= cutoffPages; page++ {
		query := url.Values{"page": {fmt.Sprint(page)}, "pageSize": {"1000"}, "monitored": {"false"}}
		var list struct {
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				ID int `json:"id"`
			} `json:"records"`
		}
		if err := client.Get("/api/v3/wanted/cutoff?"+query.Encode(), &list); err != nil {
			return nil, err
		}
		for _, r := range list.Records {
			out = append(out, r.ID)
		}
		if len(list.Records) == 0 || page*1000 >= list.TotalRecords {
			break
		}
	}
	return out, nil
}
