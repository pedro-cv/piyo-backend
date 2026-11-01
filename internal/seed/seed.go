// Package seed fills a store with demo data so the app and the admin
// endpoints have something to show in DEV_MODE. It is never used in prod.
//
// Device hashes are produced through the same hash function the API uses, so
// the demo install ids below work as X-Install-Id values when testing:
//   - "demo-device-0001" .. "demo-device-0004": ordinary devices
//   - "blocked-demo-device": blocked for 30 days
package seed

import (
	"context"
	"fmt"
	"time"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

// HashFunc maps an install id to its device hash (see httpapi.HashInstallID).
type HashFunc func(installID string) string

// Seed inserts the demo reports, NPS entries and block. It is idempotent per
// id (re-seeding an already seeded store returns nil).
func Seed(ctx context.Context, st store.Store, hash HashFunc, now time.Time) error {
	now = now.UTC()
	ago := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	ptr := func(t time.Time) *time.Time { return &t }

	d1, d2, d3, d4 := hash("demo-device-0001"), hash("demo-device-0002"), hash("demo-device-0003"), hash("demo-device-0004")
	blocked := hash("blocked-demo-device")

	reports := []model.Report{
		{ID: "r_seed00000001", DeviceHash: d1, LineID: "76", Type: model.ReportTypeStop,
			Lat: -17.7833, Lng: -63.1821, Comment: "La parada ya no está frente a la plaza, la movieron media cuadra.",
			AppVersion: "1", DataVersion: 12, Status: model.StatusPending, CreatedAt: ago(2)},
		{ID: "r_seed00000002", DeviceHash: d2, LineID: "18", Type: model.ReportTypeRoute,
			Lat: -17.7630, Lng: -63.1850, Comment: "Por Ventura Mall ahora dobla por la Av. San Martín, no sigue recto.",
			AppVersion: "1", DataVersion: 12, Status: model.StatusPending, CreatedAt: ago(5)},
		{ID: "r_seed00000003", DeviceHash: d3, LineID: "76", Type: model.ReportTypeGone,
			Lat: -17.7700, Lng: -63.1900, Comment: "Hace semanas que no pasa por acá.",
			AppVersion: "1", DataVersion: 11, Status: model.StatusAccepted, CreatedAt: ago(30), ReviewedAt: ptr(ago(20))},
		{ID: "r_seed00000004", DeviceHash: d4, Type: model.ReportTypeMissing,
			Lat: -17.8100, Lng: -63.1620, Comment: "Falta el trufi del Plan 3000 que va al centro por la Av. Paurito.",
			AppVersion: "1", DataVersion: 12, Status: model.StatusPending, CreatedAt: ago(26)},
		{ID: "r_seed00000005", DeviceHash: blocked, LineID: "18", Type: model.ReportTypeOther,
			Lat: -17.7860, Lng: -63.1900, Comment: "asdf",
			AppVersion: "1", DataVersion: 12, Status: model.StatusRejected, CreatedAt: ago(100), ReviewedAt: ptr(ago(90))},
		{ID: "r_seed00000006", DeviceHash: blocked, LineID: "76", Type: model.ReportTypeStop,
			Lat: -17.7790, Lng: -63.1835,
			AppVersion: "1", DataVersion: 12, Status: model.StatusRejected, CreatedAt: ago(80), ReviewedAt: ptr(ago(70))},
		{ID: "r_seed00000007", DeviceHash: blocked, LineID: "76", Type: model.ReportTypeRoute,
			Lat: -17.7740, Lng: -63.1850,
			AppVersion: "1", DataVersion: 12, Status: model.StatusRejected, CreatedAt: ago(60), ReviewedAt: ptr(ago(50))},
	}
	for i := range reports {
		if err := st.CreateReport(ctx, &reports[i]); err != nil && err != store.ErrAlreadyExists {
			return fmt.Errorf("seed report %s: %w", reports[i].ID, err)
		}
	}

	nps := []model.NPS{
		{ID: "n_seed00000001", DeviceHash: d1, Score: 9, Reasons: []string{model.NPSReasonMeSirvio},
			Comment: "Me ahorró preguntar en la calle.", AppVersion: "1", DataVersion: 12, DeviceClass: "android-mid", CreatedAt: ago(3)},
		{ID: "n_seed00000002", DeviceHash: d2, Score: 6, Reasons: []string{model.NPSReasonFaltanLineas, model.NPSReasonRecorridosMal},
			Comment: "Faltan micros de la zona norte.", AppVersion: "1", DataVersion: 12, DeviceClass: "android-low", CreatedAt: ago(8)},
		{ID: "n_seed00000003", DeviceHash: d3, Score: 3, Reasons: []string{model.NPSReasonEsLento, model.NPSReasonElMapa},
			Comment: "", AppVersion: "1", DataVersion: 11, DeviceClass: "android-low", CreatedAt: ago(40)},
		{ID: "n_seed00000004", DeviceHash: d4, Score: 10, Reasons: []string{model.NPSReasonMeSirvio},
			Comment: "Excelente, gracias.", AppVersion: "1", DataVersion: 12, DeviceClass: "ios", CreatedAt: ago(50)},
		{ID: "n_seed00000005", DeviceHash: blocked, Score: 7, Reasons: []string{model.NPSReasonOtraCosa},
			Comment: "Estaría bueno ver los horarios.", AppVersion: "1", DataVersion: 12, DeviceClass: "android-mid", CreatedAt: ago(70)},
	}
	for i := range nps {
		if err := st.CreateNPS(ctx, &nps[i]); err != nil && err != store.ErrAlreadyExists {
			return fmt.Errorf("seed nps %s: %w", nps[i].ID, err)
		}
	}

	suggestions := []model.Suggestion{
		{ID: "s_seed00000001", DeviceHash: d1, Kind: model.SuggestionStopAdd, LineID: "10", VariantID: "osm-6945575", DatasetVersion: 13,
			Lat: -17.7716, Lng: -63.1741, Payload: []byte(`{"name":"Av. Cristóbal de Mendoza y Beni","points":[[-17.7716,-63.1741]]}`),
			Comment: "Acá para siempre el 10, frente a la farmacia.", AppVersion: "1", Status: model.StatusPending, CreatedAt: ago(4)},
		{ID: "s_seed00000002", DeviceHash: d2, Kind: model.SuggestionPolyline, LineID: "18", VariantID: "osm-13461363", DatasetVersion: 13,
			Lat: -17.7630, Lng: -63.1850, Payload: []byte(`{"points":[[-17.7640,-63.1840],[-17.7632,-63.1848],[-17.7625,-63.1858]],"note":"dobla por San Martín"}`),
			Comment: "El recorrido real dobla por la Av. San Martín.", AppVersion: "1", Status: model.StatusPending, CreatedAt: ago(9)},
	}
	for i := range suggestions {
		if err := st.CreateSuggestion(ctx, &suggestions[i]); err != nil && err != store.ErrAlreadyExists {
			return fmt.Errorf("seed suggestion %s: %w", suggestions[i].ID, err)
		}
	}

	block := model.Block{
		DeviceHash: blocked,
		Until:      now.AddDate(0, 0, 30),
		Reason:     "3 rejected reports (seed)",
		CreatedAt:  now,
	}
	if err := st.SetBlock(ctx, block); err != nil {
		return fmt.Errorf("seed block: %w", err)
	}
	return nil
}
