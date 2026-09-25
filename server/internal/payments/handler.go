package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func (h Handler) ReturnPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	message := "You can close this window and return to Omi."
	if strings.HasSuffix(r.URL.Path, "/cancel") {
		message = "Payment was cancelled. You can close this window and return to Omi."
	}
	_, _ = io.WriteString(w, "<!doctype html><html><body><h1>Omi payments</h1><p>"+template.HTMLEscapeString(message)+"</p></body></html>")
}

type plan struct {
	ID          string `json:"id"`
	PlanID      string `json:"plan_id"`
	Title       string `json:"title"`
	PriceString string `json:"price_string"`
	Description string `json:"description,omitempty"`
	Interval    string `json:"interval"`
	UnitAmount  int    `json:"unit_amount"`
	IsActive    bool   `json:"is_active"`
}

func out(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func uid(r *http.Request) (string, error) { return auth.UserID(r.Context()) }
func plans() []plan {
	items := []plan{}
	add := func(envID, envAmount, planID, title, interval string) {
		id := os.Getenv(envID)
		if id == "" {
			return
		}
		amount, _ := strconv.Atoi(os.Getenv(envAmount))
		items = append(items, plan{ID: id, PlanID: planID, Title: title, PriceString: "$" + strconv.Itoa(amount/100) + "/" + interval, Interval: interval, UnitAmount: amount})
	}
	add("STRIPE_UNLIMITED_MONTHLY_PRICE_ID", "STRIPE_UNLIMITED_MONTHLY_AMOUNT", "unlimited", "Unlimited Monthly", "month")
	add("STRIPE_UNLIMITED_ANNUAL_PRICE_ID", "STRIPE_UNLIMITED_ANNUAL_AMOUNT", "unlimited", "Unlimited Annual", "year")
	add("STRIPE_OPERATOR_MONTHLY_PRICE_ID", "STRIPE_OPERATOR_MONTHLY_AMOUNT", "operator", "Operator Monthly", "month")
	add("STRIPE_OPERATOR_ANNUAL_PRICE_ID", "STRIPE_OPERATOR_ANNUAL_AMOUNT", "operator", "Operator Annual", "year")
	add("STRIPE_ARCHITECT_MONTHLY_PRICE_ID", "STRIPE_ARCHITECT_MONTHLY_AMOUNT", "architect", "Architect Monthly", "month")
	add("STRIPE_ARCHITECT_ANNUAL_PRICE_ID", "STRIPE_ARCHITECT_ANNUAL_AMOUNT", "architect", "Architect Annual", "year")
	return items
}
func (h Handler) AvailablePlans(w http.ResponseWriter, r *http.Request) {
	out(w, 200, map[string]any{"plans": plans()})
}

func (h Handler) OverageInfo(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	planID := "basic"
	var current sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT plan FROM subscriptions WHERE user_external_uid=?`, u).Scan(&current); err == nil && current.Valid && current.String != "" {
		planID = current.String
	}
	var questions int
	var cost float64
	var reset int64
	if rowErr := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(questions),0),COALESCE(SUM(cost_micro_usd),0) FROM llm_usage WHERE user_external_uid=? AND allocation='chat' AND created_at>=DATE_FORMAT(UTC_TIMESTAMP(),'%Y-%m-01')`, u).Scan(&questions, &cost); rowErr != nil && rowErr != sql.ErrNoRows {
		http.Error(w, rowErr.Error(), 500)
		return
	}
	now := time.Now().UTC()
	reset = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).Unix()
	markup := 1.15
	if raw := os.Getenv("OVERAGE_MARKUP_MULTIPLIER"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 1 {
			markup = v
		}
	}
	var includedQ any
	var includedCost any
	overagePlan := false
	excess := 0
	overage := 0.0
	display := map[string]string{"basic": "Free", "operator": "Operator", "architect": "Architect", "unlimited": "Neo"}[planID]
	if display == "" {
		display = planID
	}
	switch planID {
	case "operator":
		includedQ = 500
		overagePlan = true
	case "unlimited":
		includedQ = 200
		overagePlan = true
	case "architect":
		includedCost = 400.0
		overagePlan = true
	}
	if q, ok := includedQ.(int); ok && questions > q {
		excess = questions - q
		if questions > 0 {
			overage = (float64(excess) / float64(questions)) * cost * markup
		}
	}
	if c, ok := includedCost.(float64); ok && cost > c {
		overage = (cost - c) * markup
	}
	out(w, 200, map[string]any{"plan": display, "plan_type": planID, "is_overage_plan": overagePlan, "included_questions": includedQ, "included_cost_usd": includedCost, "used_questions": questions, "excess_questions": excess, "real_cost_usd": round4(cost), "overage_usd": round4(overage), "markup_multiplier": markup, "markup_percent": round2((markup - 1) * 100), "reset_at": reset, "explainer_title": "What happens past your monthly limit?", "explainer_body": "Your paid plan includes a monthly AI-usage allowance. If you go over, you stay functional and pay only for extra usage at the configured markup.", "provider_reference_rates": map[string]float64{"claude_sonnet_input_per_mtok": 3, "claude_sonnet_output_per_mtok": 15, "gemini_flash_input_per_mtok": 0.3, "gemini_flash_output_per_mtok": 2.5, "gpt_4_1_mini_input_per_mtok": 0.4, "gpt_4_1_mini_output_per_mtok": 1.6, "deepgram_nova_per_min": 0.0043}, "byok_available": true})
}
func round4(v float64) float64 { return float64(int64(v*10000+0.5)) / 10000 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
func (h Handler) Subscription(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var planName, status, subID, price sql.NullString
	var start, end sql.NullInt64
	var cancel bool
	e = h.DB.QueryRowContext(r.Context(), `SELECT plan,status,stripe_subscription_id,current_period_start,current_period_end,cancel_at_period_end,current_price_id FROM subscriptions WHERE user_external_uid=?`, u).Scan(&planName, &status, &subID, &start, &end, &cancel, &price)
	if e == sql.ErrNoRows {
		out(w, 200, map[string]any{"subscription": map[string]any{"plan": "basic", "status": "active", "cancel_at_period_end": false, "features": []string{}, "limits": map[string]any{}}})
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]any{"subscription": map[string]any{"plan": planName.String, "status": status.String, "stripe_subscription_id": subID.String, "current_period_start": start.Int64, "current_period_end": end.Int64, "cancel_at_period_end": cancel, "current_price_id": price.String, "features": []string{}, "limits": map[string]any{}}})
}
func (h Handler) PayPal(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if r.Method == http.MethodGet {
		var email, url sql.NullString
		e = h.DB.QueryRowContext(r.Context(), `SELECT email,paypalme_url FROM payment_methods WHERE user_external_uid=? AND method='paypal'`, u).Scan(&email, &url)
		if e == sql.ErrNoRows {
			out(w, 200, nil)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		out(w, 200, map[string]string{"email": email.String, "paypalme_url": url.String})
		return
	}
	var in struct {
		Email string `json:"email"`
		URL   string `json:"paypalme_url"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !strings.Contains(in.Email, "@") {
		http.Error(w, "invalid PayPal details", 400)
		return
	}
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO payment_methods(user_external_uid,method,email,paypalme_url,created_at,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE email=VALUES(email),paypalme_url=VALUES(paypalme_url),updated_at=VALUES(updated_at)`, u, "paypal", in.Email, in.URL, time.Now().UTC(), time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]string{"status": "ok"})
}
func (h Handler) Methods(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var paypal, def sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT method FROM payment_methods WHERE user_external_uid=? AND method='paypal' LIMIT 1`, u).Scan(&paypal)
	_ = h.DB.QueryRowContext(r.Context(), `SELECT default_method FROM payment_defaults WHERE user_external_uid=?`, u).Scan(&def)
	out(w, 200, map[string]any{"stripe": map[string]any{"status": "unconfigured"}, "paypal": map[string]any{"status": map[bool]string{true: "configured", false: "unconfigured"}[paypal.Valid]}, "default": func() any {
		if def.Valid {
			return def.String
		}
		return nil
	}()})
}
func (h Handler) Default(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Method string `json:"method"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Method != "stripe" && in.Method != "paypal" {
		http.Error(w, "invalid payment method", 400)
		return
	}
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO payment_defaults(user_external_uid,default_method,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE default_method=VALUES(default_method),updated_at=VALUES(updated_at)`, u, in.Method, time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]string{"status": "ok"})
}

func stripeSecret() string { return strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")) }
func (h Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		PriceID       string `json:"price_id"`
		PromotionCode string `json:"promotion_code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.PriceID) == "" {
		http.Error(w, "price_id is required", 400)
		return
	}
	if stripeSecret() == "" {
		http.Error(w, "Stripe is not configured", 503)
		return
	}
	known := false
	for _, p := range plans() {
		if p.ID == in.PriceID {
			known = true
		}
	}
	if !known {
		http.Error(w, "Unknown price_id", 400)
		return
	}
	form := url.Values{"mode": {"subscription"}, "line_items[0][price]": {in.PriceID}, "line_items[0][quantity]": {"1"}, "client_reference_id": {u}, "metadata[uid]": {u}, "subscription_data[metadata][uid]": {u}, "success_url": {os.Getenv("STRIPE_SUCCESS_URL")}, "cancel_url": {os.Getenv("STRIPE_CANCEL_URL")}}
	if form.Get("success_url") == "" {
		form.Set("success_url", strings.TrimRight(os.Getenv("BASE_API_URL"), "/")+"/v1/payments/success")
	}
	if form.Get("cancel_url") == "" {
		form.Set("cancel_url", strings.TrimRight(os.Getenv("BASE_API_URL"), "/")+"/v1/payments/cancel")
	}
	if in.PromotionCode != "" {
		promo, err := h.stripeForm(r.Context(), http.MethodGet, "/promotion_codes?code="+url.QueryEscape(in.PromotionCode)+"&active=true&limit=1", nil)
		if err != nil {
			http.Error(w, "Invalid or expired promotion code.", 400)
			return
		}
		data, _ := promo["data"].([]any)
		if len(data) == 0 {
			http.Error(w, "Invalid or expired promotion code.", 400)
			return
		}
		if item, ok := data[0].(map[string]any); ok {
			if id, ok := item["id"].(string); ok {
				form.Set("discounts[0][promotion_code]", id)
			}
		}
	}
	req, e := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.stripe.com/v1/checkout/sessions", bytes.NewBufferString(form.Encode()))
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	req.SetBasicAuth(stripeSecret(), "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, string(raw), 400)
		return
	}
	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ID == "" {
		http.Error(w, "invalid Stripe response", 502)
		return
	}
	out(w, 200, map[string]any{"url": result.URL, "session_id": result.ID})
}

func (h Handler) Upgrade(w http.ResponseWriter, r *http.Request) {
	u, err := uid(r)
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		PriceID       string `json:"price_id"`
		PromotionCode string `json:"promotion_code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.PriceID) == "" {
		http.Error(w, "price_id is required", 400)
		return
	}
	var subID, currentPrice, status string
	var cancel bool
	var periodEnd sql.NullInt64
	err = h.DB.QueryRowContext(r.Context(), `SELECT stripe_subscription_id,current_price_id,status,cancel_at_period_end,current_period_end FROM subscriptions WHERE user_external_uid=?`, u).Scan(&subID, &currentPrice, &status, &cancel, &periodEnd)
	if err == sql.ErrNoRows || subID == "" {
		http.Error(w, "No active Stripe subscription found to upgrade.", 400)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if status != "active" && status != "trialing" {
		http.Error(w, "No active Stripe subscription found to upgrade.", 400)
		return
	}
	if cancel && (!periodEnd.Valid || periodEnd.Int64 > time.Now().Unix()) {
		http.Error(w, "Plan changes are available after the current subscription ends. Reactivate your current plan to keep it.", 409)
		return
	}
	if currentPrice == in.PriceID {
		http.Error(w, "You are already subscribed to this plan. Please select a different plan to upgrade or downgrade.", 400)
		return
	}
	target := false
	for _, p := range plans() {
		if p.ID == in.PriceID {
			target = true
			break
		}
	}
	if !target {
		http.Error(w, "Unknown price_id", 400)
		return
	}
	if stripeSecret() == "" {
		http.Error(w, "Stripe is not configured", 503)
		return
	}
	current, e := h.stripeForm(r.Context(), http.MethodGet, "/subscriptions/"+url.PathEscape(subID), nil)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	items, _ := current["items"].(map[string]any)
	data, _ := items["data"].([]any)
	if len(data) == 0 {
		http.Error(w, "Stripe subscription has no items", 502)
		return
	}
	item, _ := data[0].(map[string]any)
	itemID, _ := item["id"].(string)
	if itemID == "" {
		http.Error(w, "Stripe subscription item is invalid", 502)
		return
	}
	currentPlan := planForPrice(currentPrice)
	targetPlan := planForPrice(in.PriceID)
	if currentPlan == targetPlan && currentPlan != "basic" {
		start, _ := current["current_period_start"].(float64)
		end, _ := current["current_period_end"].(float64)
		created, e := h.stripeForm(r.Context(), http.MethodPost, "/subscription_schedules", url.Values{"from_subscription": {subID}})
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		scheduleID, _ := created["id"].(string)
		if scheduleID == "" {
			http.Error(w, "Stripe returned no subscription schedule ID", 502)
			return
		}
		phase := url.Values{
			"phases[0][items][0][price]": {currentPrice}, "phases[0][items][0][quantity]": {"1"},
			"phases[0][start_date]": {strconv.FormatInt(int64(start), 10)}, "phases[0][end_date]": {strconv.FormatInt(int64(end), 10)},
			"phases[1][items][0][price]": {in.PriceID}, "phases[1][items][0][quantity]": {"1"},
			"metadata[uid]": {u}, "metadata[upgrade_type]": {currentPlan + "_" + intervalForPrice(in.PriceID)},
		}
		if strings.TrimSpace(in.PromotionCode) != "" {
			promo, e := h.stripeForm(r.Context(), http.MethodGet, "/promotion_codes?code="+url.QueryEscape(in.PromotionCode)+"&active=true&limit=1", nil)
			if e != nil {
				http.Error(w, "Invalid or expired promotion code.", 400)
				return
			}
			data, _ := promo["data"].([]any)
			if len(data) == 0 {
				http.Error(w, "Invalid or expired promotion code.", 400)
				return
			}
			if p, ok := data[0].(map[string]any); ok {
				if id, ok := p["id"].(string); ok {
					phase.Set("phases[1][discounts][0][promotion_code]", id)
				}
			}
		}
		if _, e = h.stripeForm(r.Context(), http.MethodPost, "/subscription_schedules/"+url.PathEscape(scheduleID), phase); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		remaining := int64(end) - time.Now().Unix()
		days := int64(0)
		if remaining > 0 {
			days = remaining / 86400
		}
		out(w, 200, map[string]any{"status": "success", "message": fmt.Sprintf("Upgrade scheduled! Your current plan continues for %d more days, then automatically switches to %s.", days, intervalForPrice(in.PriceID)), "subscription": map[string]any{"plan": currentPlan, "status": status, "stripe_subscription_id": subID, "current_period_end": int64(end), "cancel_at_period_end": false, "current_price_id": currentPrice, "features": []string{}, "limits": map[string]any{}}, "days_remaining": days, "schedule_id": scheduleID})
		return
	}
	form := url.Values{"items[0][id]": {itemID}, "items[0][price]": {in.PriceID}, "proration_behavior": {"create_prorations"}}
	if strings.TrimSpace(in.PromotionCode) != "" {
		promo, e := h.stripeForm(r.Context(), http.MethodGet, "/promotion_codes?code="+url.QueryEscape(in.PromotionCode)+"&active=true&limit=1", nil)
		if e != nil {
			http.Error(w, "Invalid or expired promotion code.", 400)
			return
		}
		pd, _ := promo["data"].([]any)
		if len(pd) == 0 {
			http.Error(w, "Invalid or expired promotion code.", 400)
			return
		}
		if p, ok := pd[0].(map[string]any); ok {
			if id, ok := p["id"].(string); ok {
				form.Set("discounts[0][promotion_code]", id)
			}
		}
	}
	updated, e := h.stripeForm(r.Context(), http.MethodPost, "/subscriptions/"+url.PathEscape(subID), form)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	newStatus, _ := updated["status"].(string)
	if newStatus == "" {
		newStatus = status
	}
	newEnd := int64(0)
	if v, ok := updated["current_period_end"].(float64); ok {
		newEnd = int64(v)
	}
	if newEnd == 0 && periodEnd.Valid {
		newEnd = periodEnd.Int64
	}
	planID := planForPrice(in.PriceID)
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE subscriptions SET plan=?,status=?,current_price_id=?,current_period_end=?,cancel_at_period_end=0,updated_at=? WHERE user_external_uid=?`, planID, newStatus, in.PriceID, newEnd, time.Now().UTC(), u)
	days := 0
	if newEnd > time.Now().Unix() {
		days = int((newEnd - time.Now().Unix()) / 86400)
	}
	out(w, 200, map[string]any{"status": "success", "message": "Subscription updated successfully.", "days_remaining": days, "subscription": map[string]any{"plan": planID, "status": newStatus, "stripe_subscription_id": subID, "current_period_end": newEnd, "cancel_at_period_end": false, "current_price_id": in.PriceID, "features": []string{}, "limits": map[string]any{}}})
}
func (h Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var sub string
	e = h.DB.QueryRowContext(r.Context(), `SELECT stripe_subscription_id FROM subscriptions WHERE user_external_uid=? AND status IN ('active','trialing')`, u).Scan(&sub)
	if e == sql.ErrNoRows {
		http.Error(w, "No active subscription found", 404)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if stripeSecret() == "" {
		http.Error(w, "Stripe is not configured", 503)
		return
	}
	form := url.Values{"cancel_at_period_end": {"true"}}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.stripe.com/v1/subscriptions/"+url.PathEscape(sub), strings.NewReader(form.Encode()))
	req.SetBasicAuth(stripeSecret(), "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		http.Error(w, string(raw), 400)
		return
	}
	out(w, 200, map[string]string{"status": "ok", "message": "Subscription scheduled for cancellation."})
}
func verifyStripe(payload []byte, header, secret string) bool {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "t" {
			ts = kv[1]
		}
		if kv[0] == "v1" {
			sig = kv[1]
		}
	}
	if ts == "" || sig == "" {
		return false
	}
	stamp, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || absInt64(time.Now().Unix()-stamp) > 300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + "." + string(payload)))
	expected := fmt.Sprintf("%x", mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func (h Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	payload, e := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if e != nil {
		http.Error(w, "Invalid payload", 400)
		return
	}
	if !verifyStripe(payload, r.Header.Get("Stripe-Signature"), os.Getenv("STRIPE_WEBHOOK_SECRET")) {
		http.Error(w, "Invalid signature", 400)
		return
	}
	var event struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object map[string]any `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		http.Error(w, "Invalid payload", 400)
		return
	}
	if event.ID == "" {
		http.Error(w, "Invalid payload", 400)
		return
	}
	result, err := h.DB.ExecContext(r.Context(), `INSERT IGNORE INTO stripe_webhook_events(event_id,event_type,received_at) VALUES(?,?,?)`, event.ID, event.Type, time.Now().UTC())
	if err != nil {
		http.Error(w, "Webhook state unavailable", 500)
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		out(w, 200, map[string]string{"status": "success", "message": "already processed"})
		return
	}
	obj := event.Data.Object
	uidValue, _ := obj["client_reference_id"].(string)
	if uidValue == "" {
		if md, ok := obj["metadata"].(map[string]any); ok {
			uidValue, _ = md["uid"].(string)
		}
	}
	if uidValue == "" {
		writeJSON := map[string]string{"status": "ignored"}
		out(w, 200, writeJSON)
		return
	}
	subID, _ := obj["subscription"].(string)
	if event.Type == "customer.subscription.created" || event.Type == "customer.subscription.updated" || event.Type == "checkout.session.completed" {
		status, _ := obj["status"].(string)
		if status == "" {
			status = "active"
		}
		cancel, _ := obj["cancel_at_period_end"].(bool)
		customer, _ := obj["customer"].(string)
		_, price := stripePlan(obj)
		_, _ = h.DB.ExecContext(r.Context(), `INSERT INTO subscriptions(user_external_uid,plan,status,stripe_subscription_id,stripe_customer_id,cancel_at_period_end,current_price_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE plan=VALUES(plan),status=VALUES(status),stripe_subscription_id=VALUES(stripe_subscription_id),stripe_customer_id=VALUES(stripe_customer_id),cancel_at_period_end=VALUES(cancel_at_period_end),current_price_id=VALUES(current_price_id),updated_at=VALUES(updated_at)`, uidValue, price, status, subID, customer, cancel, price, time.Now().UTC(), time.Now().UTC())
	}
	if event.Type == "customer.subscription.deleted" {
		_, _ = h.DB.ExecContext(r.Context(), `UPDATE subscriptions SET plan='basic',status='active',cancel_at_period_end=0,updated_at=? WHERE user_external_uid=?`, time.Now().UTC(), uidValue)
	}
	out(w, 200, map[string]string{"status": "success"})
}

func (h Handler) ConnectWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "Invalid payload", 400)
		return
	}
	secret := strings.TrimSpace(os.Getenv("STRIPE_CONNECT_WEBHOOK_SECRET"))
	if secret == "" || !verifyStripe(payload, r.Header.Get("Stripe-Signature"), secret) {
		http.Error(w, "Invalid signature", 400)
		return
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Object map[string]any `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil || event.Type == "" {
		http.Error(w, "Invalid payload", 400)
		return
	}
	if event.Type == "account.updated" {
		account := event.Data.Object
		charges, _ := account["charges_enabled"].(bool)
		details, _ := account["details_submitted"].(bool)
		if charges && details {
			uidValue := ""
			if metadata, ok := account["metadata"].(map[string]any); ok {
				uidValue, _ = metadata["uid"].(string)
			}
			if uidValue != "" {
				var current sql.NullString
				if err := h.DB.QueryRowContext(r.Context(), `SELECT default_method FROM payment_defaults WHERE user_external_uid=?`, uidValue).Scan(&current); err != nil && err != sql.ErrNoRows {
					http.Error(w, err.Error(), 500)
					return
				}
				if !current.Valid || strings.TrimSpace(current.String) == "" {
					if _, err := h.DB.ExecContext(r.Context(), `INSERT INTO payment_defaults(user_external_uid,default_method,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE default_method=VALUES(default_method),updated_at=VALUES(updated_at)`, uidValue, "stripe", time.Now().UTC()); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				}
			}
		}
	}
	out(w, 200, map[string]string{"status": "success"})
}

func (h Handler) Portal(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var customer sql.NullString
	e = h.DB.QueryRowContext(r.Context(), `SELECT stripe_customer_id FROM subscriptions WHERE user_external_uid=?`, u).Scan(&customer)
	if e != nil || !customer.Valid || customer.String == "" {
		http.Error(w, "No Stripe customer found. Please create a subscription first.", 400)
		return
	}
	form := url.Values{"customer": {customer.String}, "return_url": {strings.TrimRight(os.Getenv("BASE_API_URL"), "/") + "/v1/payments/portal-return"}}
	if form.Get("return_url") == "/v1/payments/portal-return" {
		form.Set("return_url", "http://localhost/v1/payments/portal-return")
	}
	result, e := h.stripeForm(r.Context(), http.MethodPost, "/billing_portal/sessions", form)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	uval, _ := result["url"].(string)
	if uval == "" {
		http.Error(w, "Stripe returned no portal URL", 502)
		return
	}
	out(w, 200, map[string]string{"url": uval})
}
func (h Handler) Connect(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	country := r.URL.Query().Get("country")
	if country == "" {
		http.Error(w, "Country is required", 400)
		return
	}
	var id sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT account_id FROM stripe_connect_accounts WHERE user_external_uid=?`, u).Scan(&id)
	if !id.Valid {
		form := url.Values{"type": {"express"}, "country": {strings.ToUpper(country)}, "capabilities[card_payments][requested]": {"true"}, "capabilities[transfers][requested]": {"true"}}
		account, e := h.stripeForm(r.Context(), http.MethodPost, "/accounts", form)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		accountID, _ := account["id"].(string)
		if accountID == "" {
			http.Error(w, "Stripe returned no account ID", 502)
			return
		}
		_, e = h.DB.ExecContext(r.Context(), `INSERT INTO stripe_connect_accounts(user_external_uid,account_id,country,created_at,updated_at) VALUES(?,?,?,?,?)`, u, accountID, country, time.Now().UTC(), time.Now().UTC())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		id = sql.NullString{String: accountID, Valid: true}
	}
	link, e := h.stripeForm(r.Context(), http.MethodPost, "/account_links", url.Values{"account": {id.String}, "refresh_url": {strings.TrimRight(os.Getenv("BASE_API_URL"), "/") + "/v1/stripe/refresh/" + id.String}, "return_url": {strings.TrimRight(os.Getenv("BASE_API_URL"), "/") + "/v1/stripe/return/" + id.String}, "type": {"account_onboarding"}})
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	urlValue, _ := link["url"].(string)
	out(w, 200, map[string]string{"account_id": id.String, "url": urlValue})
}
func (h Handler) SupportedCountries(w http.ResponseWriter, r *http.Request) {
	out(w, 200, []map[string]string{{"id": "US", "name": "United States"}, {"id": "CA", "name": "Canada"}, {"id": "GB", "name": "United Kingdom"}, {"id": "AU", "name": "Australia"}, {"id": "DE", "name": "Germany"}, {"id": "FR", "name": "France"}})
}
func (h Handler) Onboarded(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var id sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT account_id FROM stripe_connect_accounts WHERE user_external_uid=?`, u).Scan(&id)
	if !id.Valid {
		out(w, 200, map[string]bool{"onboarding_complete": false})
		return
	}
	v, e := h.stripeForm(r.Context(), http.MethodGet, "/accounts/"+url.PathEscape(id.String), nil)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	complete, _ := v["details_submitted"].(bool)
	out(w, 200, map[string]bool{"onboarding_complete": complete})
}
func (h Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	id := r.PathValue("account_id")
	var owner string
	e = h.DB.QueryRowContext(r.Context(), `SELECT user_external_uid FROM stripe_connect_accounts WHERE account_id=?`, id).Scan(&owner)
	if e == sql.ErrNoRows || owner != u {
		http.Error(w, "account not found", 404)
		return
	}
	v, e := h.stripeForm(r.Context(), http.MethodPost, "/account_links", url.Values{"account": {id}, "refresh_url": {strings.TrimRight(os.Getenv("BASE_API_URL"), "/") + "/v1/stripe/refresh/" + id}, "return_url": {strings.TrimRight(os.Getenv("BASE_API_URL"), "/") + "/v1/stripe/return/" + id}, "type": {"account_onboarding"}})
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	link, _ := v["url"].(string)
	out(w, 200, map[string]string{"account_id": id, "url": link})
}
func (h Handler) AppSubscription(w http.ResponseWriter, r *http.Request) {
	u, e := uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	appID := r.PathValue("app_id")
	var customer sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT stripe_customer_id FROM subscriptions WHERE user_external_uid=?`, u).Scan(&customer)
	if !customer.Valid || customer.String == "" {
		if r.Method == http.MethodDelete {
			http.Error(w, "Active subscription not found for this app", 404)
			return
		}
		out(w, 200, map[string]any{"subscription": nil})
		return
	}
	list, e := h.stripeForm(r.Context(), http.MethodGet, "/subscriptions?customer="+url.QueryEscape(customer.String)+"&status=all&limit=100", nil)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	data, _ := list["data"].([]any)
	var match map[string]any
	for _, raw := range data {
		if sub, ok := raw.(map[string]any); ok {
			if md, ok := sub["metadata"].(map[string]any); ok && md["app_id"] == appID {
				match = sub
				break
			}
		}
	}
	if match == nil {
		if r.Method == http.MethodDelete {
			http.Error(w, "Active subscription not found for this app", 404)
			return
		}
		out(w, 200, map[string]any{"subscription": nil})
		return
	}
	if r.Method == http.MethodDelete {
		id, _ := match["id"].(string)
		_, e = h.stripeForm(r.Context(), http.MethodPost, "/subscriptions/"+url.PathEscape(id), url.Values{"cancel_at_period_end": {"true"}})
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		out(w, 200, map[string]any{"status": "success", "message": "Subscription scheduled for cancellation at the end of the current billing period", "cancel_at_period_end": true, "current_period_end": match["current_period_end"]})
		return
	}
	items, _ := match["items"].(map[string]any)
	itemData, _ := items["data"].([]any)
	var price any
	if len(itemData) > 0 {
		if item, ok := itemData[0].(map[string]any); ok {
			if p, ok := item["price"].(map[string]any); ok {
				price = p["id"]
			}
		}
	}
	out(w, 200, map[string]any{"subscription": map[string]any{"id": match["id"], "status": match["status"], "current_period_end": match["current_period_end"], "cancel_at_period_end": match["cancel_at_period_end"], "price_id": price, "customer_id": match["customer"]}})
}
func (h Handler) stripeForm(ctx context.Context, method, path string, form url.Values) (map[string]any, error) {
	if stripeSecret() == "" {
		return nil, fmt.Errorf("Stripe is not configured")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1"+path, body)
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(stripeSecret(), "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("stripe returned %s: %s", resp.Status, string(raw))
	}
	var v map[string]any
	if e = json.Unmarshal(raw, &v); e != nil {
		return nil, e
	}
	return v, nil
}
func stripePlan(obj map[string]any) (string, string) {
	items, _ := obj["items"].(map[string]any)
	data, _ := items["data"].([]any)
	if len(data) > 0 {
		if item, ok := data[0].(map[string]any); ok {
			if p, ok := item["price"].(map[string]any); ok {
				if id, ok := p["id"].(string); ok {
					return planForPrice(id), id
				}
			}
		}
	}
	if id, ok := obj["price_id"].(string); ok {
		return planForPrice(id), id
	}
	return "basic", ""
}
func planForPrice(id string) string {
	for _, p := range plans() {
		if p.ID == id {
			return p.PlanID
		}
	}
	return "basic"
}

func intervalForPrice(id string) string {
	for _, p := range plans() {
		if p.ID == id {
			return p.Interval
		}
	}
	return "billing period"
}
