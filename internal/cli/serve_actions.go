package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

type triageActionPlanRequest struct {
	RecommendationID string          `json:"recommendation_id"`
	Plan             json.RawMessage `json:"plan,omitempty"`
	Confirm          bool            `json:"confirm,omitempty"`
	Status           string          `json:"status,omitempty"`
	Reason           string          `json:"reason,omitempty"`
}

type triageActionPlanResponse struct {
	Plan         actionPlan          `json:"plan"`
	Review       triageActionReview  `json:"review"`
	AuditEntries []triageActionAudit `json:"audit_entries"`
	Stdout       string              `json:"stdout,omitempty"`
}

type triageActionBatchApplyRequest struct {
	Actions []triageActionPlanRequest `json:"actions"`
	Confirm bool                      `json:"confirm,omitempty"`
	Reason  string                    `json:"reason,omitempty"`
}

type triageActionBatchApplyResponse struct {
	Results []triageActionPlanResponse `json:"results"`
}

type triageActionAudit struct {
	AuditID          int64  `json:"audit_id"`
	RecommendationID string `json:"recommendation_id"`
	Action           string `json:"action"`
	Status           string `json:"status"`
	Detail           string `json:"detail,omitempty"`
	CreatedAt        string `json:"created_at"`
}

func (s *serveServer) handleActions(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/actions/"), "/")
	if !s.authorizeAPIWrite(w, r) {
		return
	}
	switch rest {
	case "plan":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleActionPlan(w, r)
	case "apply":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleActionApply(w, r)
	case "apply-batch":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleActionApplyBatch(w, r)
	case "review":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleActionReview(w, r)
	default:
		http.Error(w, "unknown action endpoint", http.StatusNotFound)
	}
}

func (s *serveServer) handleActionPlan(w http.ResponseWriter, r *http.Request) {
	var req triageActionPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	_, plan, err := s.actionPlanForRecommendation(req.RecommendationID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	review, err := ensureTriageActionReview(s.home, req.RecommendationID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if err := insertTriageActionAudit(s.home, req.RecommendationID, "preview", plan.Status, firstActionDetail(strings.Join(plan.Blockers, "; "), "dry-run plan previewed")); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	audit, err := loadTriageActionAudit(s.home, req.RecommendationID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, triageActionPlanResponse{Plan: plan, Review: review, AuditEntries: audit})
}

func (s *serveServer) handleActionReview(w http.ResponseWriter, r *http.Request) {
	var req triageActionPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	switch status {
	case "accepted", "ignored", "new":
	default:
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid review status: %s", status))
		return
	}
	if _, _, err := s.actionPlanForRecommendation(req.RecommendationID); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	review, err := saveTriageActionReview(s.home, req.RecommendationID, status, req.Reason, "")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if err := insertTriageActionAudit(s.home, req.RecommendationID, "review", status, req.Reason); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, review)
}

func (s *serveServer) handleActionApply(w http.ResponseWriter, r *http.Request) {
	var req triageActionPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if !req.Confirm {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("confirm=true is required before applying an action plan"))
		return
	}
	if len(req.Plan) == 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("precomputed dry-run plan is required"))
		return
	}
	var submitted actionPlan
	if err := json.Unmarshal(req.Plan, &submitted); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid action plan: %w", err))
		return
	}
	inventory, current, err := s.actionPlanForRecommendation(req.RecommendationID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if !sameActionPlan(submitted, current) {
		writeAPIError(w, http.StatusConflict, fmt.Errorf("submitted plan no longer matches the current dry-run plan"))
		return
	}
	if current.Status != "ready" || len(current.Blockers) > 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("plan is not ready: %s", strings.Join(current.Blockers, "; ")))
		return
	}
	var stdout bytes.Buffer
	err = s.applyActionPlansInHome(inventory, []actionPlan{current}, &stdout)
	if err != nil {
		review, saveErr := saveTriageActionReview(s.home, req.RecommendationID, "failed", req.Reason, err.Error())
		if saveErr != nil {
			writeAPIError(w, http.StatusInternalServerError, saveErr)
			return
		}
		_ = insertTriageActionAudit(s.home, req.RecommendationID, "apply", "failed", err.Error())
		audit, _ := loadTriageActionAudit(s.home, req.RecommendationID)
		writeJSONResponse(w, triageActionPlanResponse{Plan: current, Review: review, AuditEntries: audit, Stdout: stdout.String()})
		return
	}
	review, err := saveTriageActionReview(s.home, req.RecommendationID, "applied", req.Reason, "")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if err := insertTriageActionAudit(s.home, req.RecommendationID, "apply", "applied", stdout.String()); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	audit, err := loadTriageActionAudit(s.home, req.RecommendationID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSONResponse(w, triageActionPlanResponse{Plan: current, Review: review, AuditEntries: audit, Stdout: stdout.String()})
}

func (s *serveServer) handleActionApplyBatch(w http.ResponseWriter, r *http.Request) {
	var req triageActionBatchApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if !req.Confirm {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("confirm=true is required before applying action plans"))
		return
	}
	if len(req.Actions) == 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("at least one action plan is required"))
		return
	}

	s.runMu.Lock()
	restore := setSkillsManagerHome(s.home)
	defer restore()
	defer s.runMu.Unlock()

	inventory, err := loadDiscoverOutputFromState(s.home)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	currents := make([]actionPlan, 0, len(req.Actions))
	seen := map[string]bool{}
	for _, action := range req.Actions {
		id := strings.TrimSpace(action.RecommendationID)
		if id == "" {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("recommendation_id required"))
			return
		}
		if seen[id] {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("duplicate recommendation_id: %s", id))
			return
		}
		seen[id] = true
		if len(action.Plan) == 0 {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("precomputed dry-run plan is required for %s", id))
			return
		}
		var submitted actionPlan
		if err := json.Unmarshal(action.Plan, &submitted); err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid action plan for %s: %w", id, err))
			return
		}
		plans, err := buildActionPlans(inventory, planOptions{inventory: "persisted-state", recommendation: id})
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if len(plans) != 1 {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("expected one plan for %s", id))
			return
		}
		current := plans[0]
		if !sameActionPlan(submitted, current) {
			writeAPIError(w, http.StatusConflict, fmt.Errorf("submitted plan no longer matches the current dry-run plan for %s", id))
			return
		}
		if current.Status != "ready" || len(current.Blockers) > 0 {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("plan is not ready for %s: %s", id, strings.Join(current.Blockers, "; ")))
			return
		}
		currents = append(currents, current)
	}

	results := make([]triageActionPlanResponse, 0, len(currents))
	for _, current := range currents {
		var stdout bytes.Buffer
		err := applyActionPlans(inventory, []actionPlan{current}, &stdout)
		reason := firstActionDetail(req.Reason, "confirmed in setup wizard")
		if err != nil {
			review, saveErr := saveTriageActionReview(s.home, current.RecommendationID, "failed", reason, err.Error())
			if saveErr != nil {
				writeAPIError(w, http.StatusInternalServerError, saveErr)
				return
			}
			_ = insertTriageActionAudit(s.home, current.RecommendationID, "apply", "failed", err.Error())
			audit, _ := loadTriageActionAudit(s.home, current.RecommendationID)
			results = append(results, triageActionPlanResponse{Plan: current, Review: review, AuditEntries: audit, Stdout: stdout.String()})
			continue
		}
		review, err := saveTriageActionReview(s.home, current.RecommendationID, "applied", reason, "")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		if err := insertTriageActionAudit(s.home, current.RecommendationID, "apply", "applied", stdout.String()); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		audit, err := loadTriageActionAudit(s.home, current.RecommendationID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		results = append(results, triageActionPlanResponse{Plan: current, Review: review, AuditEntries: audit, Stdout: stdout.String()})
	}
	writeJSONResponse(w, triageActionBatchApplyResponse{Results: results})
}

func (s *serveServer) actionPlanForRecommendation(recommendationID string) (discoverOutput, actionPlan, error) {
	recommendationID = strings.TrimSpace(recommendationID)
	if recommendationID == "" {
		return discoverOutput{}, actionPlan{}, fmt.Errorf("recommendation_id required")
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	restore := setSkillsManagerHome(s.home)
	defer restore()
	inventory, err := loadDiscoverOutputFromState(s.home)
	if err != nil {
		return discoverOutput{}, actionPlan{}, err
	}
	plans, err := buildActionPlans(inventory, planOptions{inventory: "persisted-state", recommendation: recommendationID})
	if err != nil {
		return discoverOutput{}, actionPlan{}, err
	}
	if len(plans) != 1 {
		return discoverOutput{}, actionPlan{}, fmt.Errorf("expected one plan for %s", recommendationID)
	}
	return inventory, plans[0], nil
}

func (s *serveServer) applyActionPlansInHome(inventory discoverOutput, plans []actionPlan, stdout *bytes.Buffer) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	restore := setSkillsManagerHome(s.home)
	defer restore()
	return applyActionPlans(inventory, plans, stdout)
}

func setSkillsManagerHome(home string) func() {
	prevHome := os.Getenv("SKILLS_MANAGER_HOME")
	os.Setenv("SKILLS_MANAGER_HOME", home)
	return func() {
		if prevHome == "" {
			os.Unsetenv("SKILLS_MANAGER_HOME")
		} else {
			os.Setenv("SKILLS_MANAGER_HOME", prevHome)
		}
	}
}

func sameActionPlan(a, b actionPlan) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

func ensureTriageActionReview(home, recommendationID string) (triageActionReview, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	db, err := state.Open(home)
	if err != nil {
		return triageActionReview{}, err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT OR IGNORE INTO dashboard_action_reviews (recommendation_id, status, reason, error_detail, updated_at)
VALUES (?, 'new', '', '', ?)`, recommendationID, now)
	if err != nil {
		return triageActionReview{}, err
	}
	var review triageActionReview
	err = db.QueryRow(`SELECT recommendation_id, status, COALESCE(reason, ''), COALESCE(error_detail, ''), updated_at
FROM dashboard_action_reviews WHERE recommendation_id=?`, recommendationID).Scan(
		&review.RecommendationID, &review.Status, &review.Reason, &review.ErrorDetail, &review.UpdatedAt,
	)
	return review, err
}

func saveTriageActionReview(home, recommendationID, status, reason, errorDetail string) (triageActionReview, error) {
	if strings.TrimSpace(recommendationID) == "" {
		return triageActionReview{}, errors.New("recommendation_id required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	db, err := state.Open(home)
	if err != nil {
		return triageActionReview{}, err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO dashboard_action_reviews (recommendation_id, status, reason, error_detail, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(recommendation_id) DO UPDATE SET status=excluded.status, reason=excluded.reason, error_detail=excluded.error_detail, updated_at=excluded.updated_at`,
		recommendationID, status, reason, errorDetail, now)
	if err != nil {
		return triageActionReview{}, err
	}
	return triageActionReview{RecommendationID: recommendationID, Status: status, Reason: reason, ErrorDetail: errorDetail, UpdatedAt: now}, nil
}

func insertTriageActionAudit(home, recommendationID, action, status, detail string) error {
	db, err := state.Open(home)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO dashboard_action_audit (recommendation_id, action, status, detail, created_at)
VALUES (?, ?, ?, ?, ?)`, recommendationID, action, status, detail, time.Now().UTC().Format(time.RFC3339))
	return err
}

func loadTriageActionAudit(home, recommendationID string) ([]triageActionAudit, error) {
	db, err := state.Open(home)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT audit_id, recommendation_id, action, status, COALESCE(detail, ''), created_at
FROM dashboard_action_audit WHERE recommendation_id=? ORDER BY audit_id`, recommendationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []triageActionAudit{}
	for rows.Next() {
		var item triageActionAudit
		if err := rows.Scan(&item.AuditID, &item.RecommendationID, &item.Action, &item.Status, &item.Detail, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func firstActionDetail(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
