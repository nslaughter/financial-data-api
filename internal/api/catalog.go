package api

import (
	"net/http"
	"slices"
	"sort"

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

// catalog holds the datasets and series, which never change, each sorted by
// its identifier, the order in which they are listed.
type catalog struct {
	datasets []fixtures.Dataset
	series   []fixtures.Series
}

func newCatalog(f *fixtures.Fixtures) *catalog {
	c := &catalog{datasets: slices.Clone(f.Datasets), series: slices.Clone(f.Series)}
	sort.Slice(c.datasets, func(i, j int) bool { return c.datasets[i].DatasetID < c.datasets[j].DatasetID })
	sort.Slice(c.series, func(i, j int) bool { return c.series[i].SeriesID < c.series[j].SeriesID })
	return c
}

// lookupDataset returns the dataset whose dataset_id is id.
func (c *catalog) lookupDataset(id string) (fixtures.Dataset, bool) {
	i := slices.IndexFunc(c.datasets, func(d fixtures.Dataset) bool { return d.DatasetID == id })
	if i < 0 {
		return fixtures.Dataset{}, false
	}
	return c.datasets[i], true
}

// lookupSeries returns the series whose series_id is id.
func (c *catalog) lookupSeries(id string) (fixtures.Series, bool) {
	i := slices.IndexFunc(c.series, func(sr fixtures.Series) bool { return sr.SeriesID == id })
	if i < 0 {
		return fixtures.Series{}, false
	}
	return c.series[i], true
}

// meta describes the server. It requires no key and ignores the
// Authorization header.
func (s *Server) meta(w http.ResponseWriter, c *call) *problem {
	writeJSON(w, http.StatusOK, metaResponse{
		APIVersion:           apiVersion,
		SupportedAPIVersions: []string{apiVersion},
		ContractVersion:      s.contractVersion,
		ServerTime:           formatTimestamp(c.now),
	})
	return nil
}

func (s *Server) listDatasets(w http.ResponseWriter, c *call) *problem {
	data := make([]datasetResponse, 0, len(s.catalog.datasets))
	for _, d := range s.catalog.datasets {
		data = append(data, s.dataset(d, c))
	}
	writeJSON(w, http.StatusOK, list[datasetResponse]{Data: data})
	return nil
}

func (s *Server) getDataset(w http.ResponseWriter, c *call) *problem {
	id := c.path["dataset_id"]
	d, ok := s.catalog.lookupDataset(id)
	if !ok {
		return notFound("There is no dataset %q.", id)
	}
	writeJSON(w, http.StatusOK, s.dataset(d, c))
	return nil
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

// entitledDataset returns the dataset the request's path names, after
// looking it up and then checking that the credential is entitled to it.
func (s *Server) entitledDataset(c *call) (fixtures.Dataset, *problem) {
	id := c.path["dataset_id"]
	d, ok := s.catalog.lookupDataset(id)
	if !ok {
		return fixtures.Dataset{}, notFound("There is no dataset %q.", id)
	}
	if !c.cred.entitled(id) {
		return fixtures.Dataset{}, notEntitled(id)
	}
	return d, nil
}

func (s *Server) listSeries(w http.ResponseWriter, c *call) *problem {
	data := make([]seriesResponse, 0, len(s.catalog.series))
	for _, sr := range s.catalog.series {
		data = append(data, seriesOf(sr, c.cred))
	}
	writeJSON(w, http.StatusOK, list[seriesResponse]{Data: data})
	return nil
}

func (s *Server) getSeries(w http.ResponseWriter, c *call) *problem {
	id := c.path["series_id"]
	sr, ok := s.catalog.lookupSeries(id)
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
