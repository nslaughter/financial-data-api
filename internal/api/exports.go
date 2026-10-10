package api

import (
	"log"
	"net/http"
	"strconv"

	"github.com/nslaughter/financial-data-api/internal/exports"
)

// ndjsonType is the media type of an export file.
const ndjsonType = "application/x-ndjson"

// manifestResponse is an export's manifest.
type manifestResponse struct {
	ExportID        string               `json:"export_id"`
	DatasetID       string               `json:"dataset_id"`
	Position        int64                `json:"position"`
	CreatedAt       string               `json:"created_at"`
	ExpiresAt       string               `json:"expires_at"`
	APIVersion      string               `json:"api_version"`
	ContractVersion string               `json:"contract_version"`
	Coverage        coverageResponse     `json:"coverage"`
	Files           []exportFileResponse `json:"files"`
}

type coverageResponse struct {
	SeriesIDs        []string `json:"series_ids"`
	ObservationCount int      `json:"observation_count"`
	RevisionCount    int      `json:"revision_count"`
	PeriodStart      *string  `json:"period_start"`
	PeriodEnd        *string  `json:"period_end"`
}

type exportFileResponse struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	MediaType   string `json:"media_type"`
	RecordCount int    `json:"record_count"`
	SizeBytes   int    `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

// createExport creates an export of a dataset at its head position. The body
// is empty or {}, so any field is unknown_parameter. An export created by a
// request that read the state before a reset goes into the store the reset
// replaced, as if the reset deleted it.
func (s *Server) createExport(w http.ResponseWriter, c *call) *problem {
	d, p := s.entitledDataset(c)
	if p != nil {
		return p
	}
	id := d.DatasetID
	e, err := c.exports.Create(id, s.history.Position(id, c.now), c.now)
	if err != nil {
		log.Printf("api: creating an export of %s: %v", id, err)
		return newProblem("internal", nil, "The export could not be created.")
	}
	w.Header().Set("Location", exportPath(e.ID))
	writeJSON(w, http.StatusCreated, s.manifest(e))
	return nil
}

// getExport returns an export's manifest.
func (s *Server) getExport(w http.ResponseWriter, c *call) *problem {
	e, p := lookupExport(c)
	if p != nil {
		return p
	}
	if p := checkExportAccess(c, e); p != nil {
		return p
	}
	writeJSON(w, http.StatusOK, s.manifest(e))
	return nil
}

// downloadExportFile returns an export's file. The file is a lookup like the
// export, so an unknown file is not_found before entitlement is checked.
func (s *Server) downloadExportFile(w http.ResponseWriter, c *call) *problem {
	e, p := lookupExport(c)
	if p != nil {
		return p
	}
	if name := c.path["file_name"]; name != exports.FileName {
		return notFound("Export %s has no file %q; its one file is %s.", e.ID, name, exports.FileName)
	}
	if p := checkExportAccess(c, e); p != nil {
		return p
	}
	data := c.exports.FileBytes(e)
	h := w.Header()
	h.Set("Content-Type", ndjsonType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

// lookupExport returns the export the request's path names.
func lookupExport(c *call) (exports.Export, *problem) {
	id := c.path["export_id"]
	e, ok := c.exports.Get(id)
	if !ok {
		return exports.Export{}, notFound("There is no export %q.", id)
	}
	return e, nil
}

// checkExportAccess checks, after lookup, that the credential is entitled to
// the export's dataset, and then that the export has not expired.
func checkExportAccess(c *call, e exports.Export) *problem {
	if !c.cred.entitled(e.DatasetID) {
		return notEntitled(e.DatasetID)
	}
	if e.Expired(c.now) {
		return newProblem("export_expired", nil,
			"Export %s expired at %s, 86,400 seconds after it was created. Create a new export.",
			e.ID, formatTimestamp(e.ExpiresAt()))
	}
	return nil
}

// manifest describes an export.
func (s *Server) manifest(e exports.Export) manifestResponse {
	return manifestResponse{
		ExportID:        e.ID,
		DatasetID:       e.DatasetID,
		Position:        e.Position,
		CreatedAt:       formatTimestamp(e.CreatedAt),
		ExpiresAt:       formatTimestamp(e.ExpiresAt()),
		APIVersion:      apiVersion,
		ContractVersion: s.contractVersion,
		Coverage: coverageResponse{
			SeriesIDs:        e.Coverage.SeriesIDs,
			ObservationCount: e.Coverage.ObservationCount,
			RevisionCount:    e.Coverage.RevisionCount,
			PeriodStart:      e.Coverage.PeriodStart,
			PeriodEnd:        e.Coverage.PeriodEnd,
		},
		Files: []exportFileResponse{{
			Name:        exports.FileName,
			URL:         exportPath(e.ID) + "/files/" + exports.FileName,
			MediaType:   ndjsonType,
			RecordCount: e.File.RecordCount,
			SizeBytes:   e.File.SizeBytes,
			SHA256:      e.File.SHA256,
		}},
	}
}

// exportPath is the path of an export's manifest. An identifier needs no
// escaping in a path.
func exportPath(id string) string {
	return "/v1/exports/" + id
}
