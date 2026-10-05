package api

import (
	"net/http"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
)

// The response types keep the field order of spec/api.md, and a field
// without a value is a nil pointer, which encodes as null.

type list[T any] struct {
	Data []T `json:"data"`
}

type metaResponse struct {
	APIVersion           string   `json:"api_version"`
	SupportedAPIVersions []string `json:"supported_api_versions"`
	ContractVersion      string   `json:"contract_version"`
	ServerTime           string   `json:"server_time"`
}

type datasetResponse struct {
	DatasetID    string `json:"dataset_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Entitled     bool   `json:"entitled"`
	HeadPosition *int64 `json:"head_position"`
}

type seriesResponse struct {
	SeriesID           string `json:"series_id"`
	DatasetID          string `json:"dataset_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Frequency          string `json:"frequency"`
	Unit               string `json:"unit"`
	BasePeriod         string `json:"base_period"`
	SeasonalAdjustment string `json:"seasonal_adjustment"`
	Source             string `json:"source"`
	ReleaseSchedule    string `json:"release_schedule"`
	Entitled           bool   `json:"entitled"`
}

// meta describes the server. It requires no key and ignores the
// Authorization header.
func (s *Server) meta(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	writeJSON(w, http.StatusOK, metaResponse{
		APIVersion:           apiVersion,
		SupportedAPIVersions: []string{apiVersion},
		ContractVersion:      s.contractVersion,
		ServerTime:           formatTimestamp(c.now),
	})
	return nil
}

func (s *Server) listDatasets(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	data := make([]datasetResponse, 0, len(s.datasets))
	for _, d := range s.datasets {
		data = append(data, s.dataset(d, c))
	}
	writeJSON(w, http.StatusOK, list[datasetResponse]{Data: data})
	return nil
}

func (s *Server) getDataset(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	id := c.path["dataset_id"]
	for _, d := range s.datasets {
		if d.DatasetID == id {
			writeJSON(w, http.StatusOK, s.dataset(d, c))
			return nil
		}
	}
	return notFound("There is no dataset %q.", id)
}

// dataset describes a dataset to the requesting credential. Its head
// position is null unless the credential is entitled to the dataset.
func (s *Server) dataset(d fixtures.Dataset, c *call) datasetResponse {
	r := datasetResponse{DatasetID: d.DatasetID, Name: d.Name, Description: d.Description}
	if c.cred.entitled(d.DatasetID) {
		head := s.history.Position(d.DatasetID, c.now)
		r.Entitled, r.HeadPosition = true, &head
	}
	return r
}

// datasetExists reports whether the catalog has a dataset.
func (s *Server) datasetExists(id string) bool {
	for _, d := range s.datasets {
		if d.DatasetID == id {
			return true
		}
	}
	return false
}

func (s *Server) listSeries(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	data := make([]seriesResponse, 0, len(s.series))
	for _, sr := range s.series {
		data = append(data, seriesOf(sr, c.cred))
	}
	writeJSON(w, http.StatusOK, list[seriesResponse]{Data: data})
	return nil
}

func (s *Server) getSeries(w http.ResponseWriter, c *call) *problem {
	if _, p := parseQuery(c.r.URL.RawQuery, c.endpoint); p != nil {
		return p
	}
	id := c.path["series_id"]
	sr, ok := s.findSeries(id)
	if !ok {
		return notFound("There is no series %q.", id)
	}
	writeJSON(w, http.StatusOK, seriesOf(sr, c.cred))
	return nil
}

// seriesOf describes a series to the requesting credential.
func seriesOf(s fixtures.Series, cred *credential) seriesResponse {
	return seriesResponse{
		SeriesID:           s.SeriesID,
		DatasetID:          s.DatasetID,
		Name:               s.Name,
		Description:        s.Description,
		Frequency:          s.Frequency,
		Unit:               s.Unit,
		BasePeriod:         s.BasePeriod,
		SeasonalAdjustment: s.SeasonalAdjustment,
		Source:             s.Source,
		ReleaseSchedule:    s.ReleaseSchedule,
		Entitled:           cred.entitled(s.DatasetID),
	}
}
