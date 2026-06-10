package jobs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"barberbase-core/internal/config"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WeeklySummary struct {
	db          *pgxpool.Pool
	cfg         *config.Config
	lastRunDate string
}

func NewWeeklySummary(db *pgxpool.Pool, cfg *config.Config) *WeeklySummary {
	return &WeeklySummary{
		db:  db,
		cfg: cfg,
	}
}

func (w *WeeklySummary) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Check on startup
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *WeeklySummary) tick(ctx context.Context) {
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		log.Printf("WeeklySummary: failed to load IST location: %v", err)
		return
	}
	now := time.Now().In(ist)

	if now.Weekday() == time.Sunday && now.Hour() == 22 && now.Minute() == 0 {
		todayStr := now.Format("2006-01-02")
		if w.lastRunDate == todayStr {
			return // already run today
		}

		var acquired bool
		err := w.db.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockWeeklySummary).Scan(&acquired)
		if err != nil || !acquired {
			return
		}
		defer w.db.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockWeeklySummary)

		w.lastRunDate = todayStr
		w.RunJob(ctx, now)
	}
}

func (w *WeeklySummary) RunJob(ctx context.Context, now time.Time) {
	sunday := now.Truncate(24 * time.Hour) // Sunday 00:00 IST
	monday := sunday.AddDate(0, 0, -6)     // Monday 00:00 IST
	weekStart := monday
	weekEnd := sunday.Add(24 * time.Hour) // next Monday 00:00 IST (exclusive)

	weekStartUTC := weekStart.UTC()
	weekEndUTC := weekEnd.UTC()
	weekRange := fmt.Sprintf("%s – %s", monday.Format("Jan 2"), sunday.Format("Jan 2"))

	conn, err := w.db.Acquire(ctx)
	if err != nil {
		log.Printf("WeeklySummary: failed to acquire connection: %v", err)
		return
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET LOCAL statement_timeout = 0")
	if err != nil {
		log.Printf("WeeklySummary: failed to set statement_timeout = 0: %v", err)
		return
	}

	rows, err := conn.Query(ctx, `
		WITH visit_stats AS (
		  SELECT v.tenant_id, v.location_id,
		    COUNT(DISTINCT v.id)                                                    AS total_visits,
		    COALESCE(SUM(vp.amount_paise) FILTER (WHERE vp.voided_at IS NULL), 0)  AS total_revenue_paise
		  FROM visits v
		  LEFT JOIN visit_charges vc  ON vc.visit_id = v.id
		  LEFT JOIN visit_payments vp ON vp.visit_charge_id = vc.id
		  WHERE v.status = 'completed'
		    AND v.completed_at >= $1 AND v.completed_at < $2
		  GROUP BY v.tenant_id, v.location_id
		),
		rating_stats AS (
		  SELECT tenant_id, location_id,
		    ROUND(AVG(rating)::NUMERIC, 1) AS avg_rating
		  FROM feedback_responses
		  WHERE received_at >= $1 AND received_at < $2
		  GROUP BY tenant_id, location_id
		),
		wait_stats AS (
		  SELECT qs.tenant_id, qs.location_id,
		    ROUND(AVG(EXTRACT(EPOCH FROM (qe.started_at - qe.remote_joined_at)) / 60)::NUMERIC, 0)
		      AS avg_wait_minutes
		  FROM queue_entries qe
		  JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		  WHERE qe.state = 'completed'
		    AND qe.started_at IS NOT NULL AND qe.remote_joined_at IS NOT NULL
		    AND qe.started_at >= $1 AND qe.started_at < $2
		  GROUP BY qs.tenant_id, qs.location_id
		),
		noshow_stats AS (
		  SELECT qs.tenant_id, qs.location_id, COUNT(qe.id) AS no_show_count
		  FROM queue_entries qe
		  JOIN queue_sessions qs ON qs.id = qe.queue_session_id
		  WHERE qe.state = 'no_show'
		    AND qs.business_date >= $3 AND qs.business_date <= $4
		  GROUP BY qs.tenant_id, qs.location_id
		),
		best_day AS (
		  SELECT DISTINCT ON (v.tenant_id, v.location_id)
		    v.tenant_id, v.location_id,
		    TRIM(TO_CHAR(DATE(v.completed_at AT TIME ZONE 'Asia/Kolkata'), 'Day')) AS best_day_name,
		    COUNT(*)                                                           AS best_day_count
		  FROM visits v
		  WHERE v.status = 'completed'
		    AND v.completed_at >= $1 AND v.completed_at < $2
		  GROUP BY v.tenant_id, v.location_id,
		           DATE(v.completed_at AT TIME ZONE 'Asia/Kolkata')
		  ORDER BY v.tenant_id, v.location_id, COUNT(*) DESC
		)
		SELECT
		  t.id               AS tenant_id,
		  t.owner_phone_number,
		  l.id               AS location_id,
		  l.name             AS shop_name,
		  COALESCE(vs.total_visits, 0)        AS total_visits,
		  COALESCE(vs.total_revenue_paise, 0) AS total_revenue_paise,
		  COALESCE(rs.avg_rating, 0)          AS avg_rating,
		  COALESCE(ws.avg_wait_minutes, 0)    AS avg_wait_minutes,
		  COALESCE(ns.no_show_count, 0)       AS no_show_count,
		  COALESCE(bd.best_day_name, '')      AS best_day_name,
		  COALESCE(bd.best_day_count, 0)      AS best_day_count
		FROM tenants t
		JOIN locations l ON l.tenant_id = t.id AND l.is_active = true
		LEFT JOIN visit_stats   vs ON vs.tenant_id=t.id AND vs.location_id=l.id
		LEFT JOIN rating_stats  rs ON rs.tenant_id=t.id AND rs.location_id=l.id
		LEFT JOIN wait_stats    ws ON ws.tenant_id=t.id AND ws.location_id=l.id
		LEFT JOIN noshow_stats  ns ON ns.tenant_id=t.id AND ns.location_id=l.id
		LEFT JOIN best_day      bd ON bd.tenant_id=t.id AND bd.location_id=l.id
		WHERE t.is_active = true
	`, weekStartUTC, weekEndUTC, monday.Format("2006-01-02"), sunday.Format("2006-01-02"))
	if err != nil {
		log.Printf("WeeklySummary: query failed: %v", err)
		return
	}
	defer rows.Close()

	type summaryRow struct {
		TenantID            uuid.UUID
		OwnerPhoneNumber    string
		LocationID          uuid.UUID
		ShopName            string
		TotalVisits         int
		TotalRevenuePaise   int64
		AvgRating           float64
		AvgWaitMinutes      float64
		NoShowCount         int
		BestDayName         string
		BestDayCount        int
	}

	var data []summaryRow
	for rows.Next() {
		var r summaryRow
		err := rows.Scan(
			&r.TenantID, &r.OwnerPhoneNumber, &r.LocationID, &r.ShopName,
			&r.TotalVisits, &r.TotalRevenuePaise, &r.AvgRating, &r.AvgWaitMinutes,
			&r.NoShowCount, &r.BestDayName, &r.BestDayCount,
		)
		if err != nil {
			log.Printf("WeeklySummary: scan failed: %v", err)
			continue
		}
		data = append(data, r)
	}
	rows.Close()

	for _, r := range data {
		totalRevenueFormatted := formatIndianNumber(r.TotalRevenuePaise / 100)

		avgRatingStr := "N/A"
		if r.AvgRating > 0 {
			avgRatingStr = fmt.Sprintf("%.1f", r.AvgRating)
		}

		avgWaitStr := fmt.Sprintf("%d", int(r.AvgWaitMinutes))

		highlightText := ""
		if r.BestDayCount > 0 {
			highlightText = fmt.Sprintf("🏆 Best day: %s (%d customers)!", r.BestDayName, r.BestDayCount)
		}

		ownerToken := generateOwnerToken(r.TenantID, r.LocationID, now, []byte(w.cfg.HMACSecret))

		outboxPayload := map[string]interface{}{
			"template_code":       "bb_weekly_summary",
			"to":                  r.OwnerPhoneNumber,
			"from_business_phone": w.cfg.BhejnaFromPhone,
			"components": []interface{}{
				map[string]interface{}{
					"type": "header",
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": weekRange},
					},
				},
				map[string]interface{}{
					"type": "body",
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": weekRange},
						map[string]interface{}{"type": "text", "text": r.ShopName},
						map[string]interface{}{"type": "text", "text": totalRevenueFormatted},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(r.TotalVisits)},
						map[string]interface{}{"type": "text", "text": avgRatingStr},
						map[string]interface{}{"type": "text", "text": avgWaitStr},
						map[string]interface{}{"type": "text", "text": strconv.Itoa(r.NoShowCount)},
						map[string]interface{}{"type": "text", "text": highlightText},
					},
				},
				map[string]interface{}{
					"type":     "button",
					"sub_type": "url",
					"index":    0,
					"parameters": []interface{}{
						map[string]interface{}{"type": "text", "text": ownerToken},
					},
				},
			},
		}

		payloadBytes, err := json.Marshal(outboxPayload)
		if err != nil {
			log.Printf("WeeklySummary: marshal failed for location %s: %v", r.LocationID, err)
			continue
		}

		_, err = w.db.Exec(ctx, `
			INSERT INTO outbox_events (tenant_id, type, payload, process_after)
			VALUES ($1, 'weekly_summary.send', $2, NOW())
		`, r.TenantID, payloadBytes)
		if err != nil {
			log.Printf("WeeklySummary: insert outbox failed for location %s: %v", r.LocationID, err)
		}
	}
}

func formatIndianNumber(num int64) string {
	s := strconv.FormatInt(num, 10)
	neg := false
	if num < 0 {
		neg = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	last3 := s[len(s)-3:]
	rest := s[:len(s)-3]

	var groups []string
	for len(rest) > 2 {
		groups = append([]string{rest[len(rest)-2:]}, groups...)
		rest = rest[:len(rest)-2]
	}
	if len(rest) > 0 {
		groups = append([]string{rest}, groups...)
	}

	res := strings.Join(groups, ",") + "," + last3
	if neg {
		return "-" + res
	}
	return res
}

func generateOwnerToken(tenantID, locationID uuid.UUID, now time.Time, secret []byte) string {
	type ownerPayload struct {
		TID string `json:"tid"`
		LID string `json:"lid"`
		Exp int64  `json:"exp"`
	}
	p := ownerPayload{
		TID: tenantID.String(),
		LID: locationID.String(),
		Exp: now.Add(7 * 24 * time.Hour).Unix(),
	}
	payloadBytes, _ := json.Marshal(p)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	h := hmac.New(sha256.New, secret)
	h.Write(payloadBytes)
	mac := h.Sum(nil)
	macB64 := base64.RawURLEncoding.EncodeToString(mac)

	return payloadB64 + "." + macB64
}
