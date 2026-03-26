package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	rrSidecarBase = "http://127.0.0.1:8200"
	rrXORKey      = "faif-scanner-obfuscation-key"
)

// RR provides Radio Reference API proxy functionality.
type RR struct {
	Admin      *Admin
	Controller *Controller
}

// NewRR creates a new RR instance attached to the given controller.
func NewRR(controller *Controller) *RR {
	return &RR{
		Admin:      controller.Admin,
		Controller: controller,
	}
}

// rrConfig holds the Radio Reference credentials.
type rrConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	AppKey   string `json:"app_key"`
}

// xorObfuscate applies simple XOR-based obfuscation (not cryptographically secure).
func xorObfuscate(input string) string {
	key := []byte(rrXORKey)
	data := []byte(input)
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ key[i%len(key)]
	}
	return base64.StdEncoding.EncodeToString(out)
}

// xorDeobfuscate reverses the XOR obfuscation.
func xorDeobfuscate(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	key := []byte(rrXORKey)
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ key[i%len(key)]
	}
	return string(out), nil
}

// readRRConfig reads the RR credentials from the rdioScannerConfigs table.
func (rr *RR) readRRConfig() (*rrConfig, error) {
	db := rr.Controller.Database
	cfg := &rrConfig{}

	readVal := func(key string) (string, error) {
		var val string
		err := db.Sql.QueryRow("select `val` from `rdioScannerConfigs` where `key` = ?", key).Scan(&val)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return val, err
	}

	username, err := readVal("rr_username")
	if err != nil {
		return nil, fmt.Errorf("rr.readconfig: %v", err)
	}
	cfg.Username = username

	password, err := readVal("rr_password")
	if err != nil {
		return nil, fmt.Errorf("rr.readconfig: %v", err)
	}
	if password != "" {
		if plain, err := xorDeobfuscate(password); err == nil {
			cfg.Password = plain
		}
	}

	appKey, err := readVal("rr_app_key")
	if err != nil {
		return nil, fmt.Errorf("rr.readconfig: %v", err)
	}
	if appKey != "" {
		if plain, err := xorDeobfuscate(appKey); err == nil {
			cfg.AppKey = plain
		}
	}

	// Fall back to environment variable if no custom key stored
	if cfg.AppKey == "" && DefaultRRAppKey != "" {
		cfg.AppKey = DefaultRRAppKey
	}

	return cfg, nil
}

// saveRRCredentials saves username and password to the rdioScannerConfigs table.
func (rr *RR) saveRRCredentials(username, password string) error {
	db := rr.Controller.Database
	upsert := func(key, val string) error {
		res, err := db.Sql.Exec("update `rdioScannerConfigs` set `val` = ? where `key` = ?", val, key)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			_, err = db.Sql.Exec("insert into `rdioScannerConfigs` (`key`, `val`) values (?, ?)", key, val)
		}
		return err
	}
	if err := upsert("rr_username", username); err != nil {
		return err
	}
	obf := ""
	if password != "" {
		obf = xorObfuscate(password)
	}
	return upsert("rr_password", obf)
}

// SavedCredsHandler handles GET /api/admin/rr/saved-creds — returns saved credentials (masked)
func (rr *RR) SavedCredsHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	cfg, err := rr.readRRConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read config: %v", err), http.StatusInternalServerError)
		return
	}
	result := map[string]any{
		"has_saved": cfg.Username != "" && cfg.Password != "",
		"username":  cfg.Username,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ClearCredsHandler handles POST /api/admin/rr/clear-creds — removes saved credentials
func (rr *RR) ClearCredsHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	db := rr.Controller.Database
	db.Sql.Exec("delete from `rdioScannerConfigs` where `key` = ?", "rr_username")
	db.Sql.Exec("delete from `rdioScannerConfigs` where `key` = ?", "rr_password")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ConnectHandler handles POST /api/admin/rr/connect — optionally saves credentials and tests connection
func (rr *RR) ConnectHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Save     bool   `json:"save"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	// If password is the sentinel value, use stored password from DB
	storedCfg, _ := rr.readRRConfig()
	actualPassword := body.Password
	if body.Password == "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022" && storedCfg != nil {
		actualPassword = storedCfg.Password
	}

	log.Printf("rr.connect: Username=%q Password_len=%d save=%v usedStored=%v",
		body.Username, len(actualPassword), body.Save, actualPassword != body.Password)

	cfg := &rrConfig{
		Username: body.Username,
		Password: actualPassword,
		AppKey:   DefaultRRAppKey,
	}
	if storedCfg != nil && storedCfg.AppKey != "" {
		cfg.AppKey = storedCfg.AppKey
	}

	testURL := fmt.Sprintf("%s/health?username=%s&password=%s&app_key=%s",
		rrSidecarBase,
		url.QueryEscape(cfg.Username),
		url.QueryEscape(cfg.Password),
		url.QueryEscape(cfg.AppKey))

	resp, err := http.Get(testURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("sidecar request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		// Credentials are valid — save them
		if err := rr.saveRRCredentials(body.Username, actualPassword); err != nil {
			log.Printf("rr.connect: failed to save credentials: %v", err)
		}
		log.Printf("rr.connect: success, credentials saved for user=%s", body.Username)
	} else {
		log.Printf("rr.connect: health check failed %d for user=%s", resp.StatusCode, body.Username)
	}

	// Forward the sidecar response to client
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// requireAuth validates the admin token from the request. Returns false and
// writes a 401 response if the token is invalid.
func (rr *RR) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	t := rr.Admin.GetAuthorization(r)
	if !rr.Admin.ValidateToken(t) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// proxySidecar forwards a request to the RR Python sidecar, appending stored
// credentials as query parameters. It copies the sidecar response status code
// and body back to the client.
func (rr *RR) proxySidecar(w http.ResponseWriter, sidecarPath string) {
	cfg, err := rr.readRRConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read RR config: %v", err), http.StatusInternalServerError)
		return
	}

	reqURL := fmt.Sprintf("%s%s?username=%s&password=%s&app_key=%s",
		rrSidecarBase, sidecarPath,
		url.QueryEscape(cfg.Username),
		url.QueryEscape(cfg.Password),
		url.QueryEscape(cfg.AppKey))

	resp, err := http.Get(reqURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("sidecar request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// StatusHandler handles GET /api/admin/rr/status — reports whether an API key is available
func (rr *RR) StatusHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	hasKey := DefaultRRAppKey != ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"has_api_key": hasKey,
	})
}

// HealthHandler handles GET /api/admin/rr/health
func (rr *RR) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	rr.proxySidecar(w, "/health")
}

// CountriesHandler handles GET /api/admin/rr/countries
func (rr *RR) CountriesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	rr.proxySidecar(w, "/countries")
}

// StatesHandler handles GET /api/admin/rr/states/{id}
func (rr *RR) StatesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/states/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/states/"+id)
}

// CountiesHandler handles GET /api/admin/rr/counties/{id}
func (rr *RR) CountiesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/counties/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/counties/"+id)
}

// AgenciesHandler handles GET /api/admin/rr/agencies/{id}
func (rr *RR) AgenciesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/agencies/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/agencies/"+id)
}

// SystemsHandler handles GET /api/admin/rr/systems/{id}
func (rr *RR) SystemsHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/systems/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/systems/"+id)
}

// TalkgroupsHandler handles GET /api/admin/rr/talkgroups/{id}
func (rr *RR) TalkgroupsHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/talkgroups/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/talkgroups/"+id)
}

// UpdatesHandler handles GET /api/admin/rr/updates/{id}
func (rr *RR) UpdatesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/updates/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/updates/"+id)
}

// rrImportRequest represents the POST body for the import handler.
type rrImportRequest struct {
	SystemID   uint              `json:"system_id"`
	Talkgroups []rrImportTG      `json:"talkgroups"`
}

// rrImportTG represents a single talkgroup entry in the import request.
type rrImportTG struct {
	DecimalID   uint   `json:"decimal_id"`
	AlphaTag    string `json:"alpha_tag"`
	Description string `json:"description"`
	Tag         string `json:"tag"`
	Category    string `json:"category"`
	Frequency   uint   `json:"frequency,omitempty"`
}

// rrImportResponse is returned from the import handler.
type rrImportResponse struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Total   int `json:"total"`
}

// ImportHandler handles POST /api/admin/rr/import/{system_id}
func (rr *RR) ImportHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse system_id from URL path
	idStr := extractTrailingID(r.URL.Path, "/api/admin/rr/import/")
	if idStr == "" {
		http.Error(w, "missing system_id", http.StatusBadRequest)
		return
	}
	systemID64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		http.Error(w, "invalid system_id", http.StatusBadRequest)
		return
	}
	systemID := uint(systemID64)

	var req rrImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Use system_id from URL, falling back to body
	if systemID == 0 {
		systemID = req.SystemID
	}
	if systemID == 0 {
		http.Error(w, "system_id is required", http.StatusBadRequest)
		return
	}

	db := rr.Controller.Database
	added := 0
	updated := 0

	for _, tg := range req.Talkgroups {
		if tg.DecimalID == 0 {
			continue
		}

		// Resolve or create group by category name
		groupID, err := rr.resolveGroupID(tg.Category)
		if err != nil {
			log.Printf("rr.import: failed to resolve group %q: %v", tg.Category, err)
			http.Error(w, fmt.Sprintf("failed to resolve group: %v", err), http.StatusInternalServerError)
			return
		}

		// Resolve or create tag by tag name
		tagID, err := rr.resolveTagID(tg.Tag)
		if err != nil {
			log.Printf("rr.import: failed to resolve tag %q: %v", tg.Tag, err)
			http.Error(w, fmt.Sprintf("failed to resolve tag: %v", err), http.StatusInternalServerError)
			return
		}

		label := tg.AlphaTag
		if label == "" {
			label = fmt.Sprintf("%d", tg.DecimalID)
		}

		name := tg.Description
		if name == "" {
			name = label
		}

		// Check if talkgroup already exists for this system
		var count uint
		if err := db.Sql.QueryRow("select count(*) from `rdioScannerTalkgroups` where `id` = ? and `systemId` = ?", tg.DecimalID, systemID).Scan(&count); err != nil {
			http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
			return
		}

		if count == 0 {
			_, err = db.Sql.Exec(
				"insert into `rdioScannerTalkgroups` (`frequency`, `groupId`, `id`, `label`, `led`, `name`, `order`, `systemId`, `tagId`) values (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				tg.Frequency, groupID, tg.DecimalID, label, nil, name, 0, systemID, tagID,
			)
			if err != nil {
				http.Error(w, fmt.Sprintf("insert failed: %v", err), http.StatusInternalServerError)
				return
			}
			added++
		} else {
			_, err = db.Sql.Exec(
				"update `rdioScannerTalkgroups` set `frequency` = ?, `groupId` = ?, `label` = ?, `name` = ?, `tagId` = ? where `id` = ? and `systemId` = ?",
				tg.Frequency, groupID, label, name, tagID, tg.DecimalID, systemID,
			)
			if err != nil {
				http.Error(w, fmt.Sprintf("update failed: %v", err), http.StatusInternalServerError)
				return
			}
			updated++
		}
	}

	// Reload systems/groups/tags so in-memory state reflects DB changes
	rr.Controller.Groups.Read(rr.Controller.Database)
	rr.Controller.Tags.Read(rr.Controller.Database)
	rr.Controller.Systems.Read(rr.Controller.Database)
	rr.Controller.EmitConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rrImportResponse{
		Added:   added,
		Updated: updated,
		Total:   added + updated,
	})
}

// resolveGroupID looks up a group by label, creating it if it does not exist.
// Returns the group's _id.
func (rr *RR) resolveGroupID(label string) (uint, error) {
	if label == "" {
		label = "Unknown"
	}

	controller := rr.Controller

	// Check in-memory list first
	if group, ok := controller.Groups.GetGroup(label); ok {
		switch v := group.Id.(type) {
		case uint:
			return v, nil
		}
	}

	// Not found, create it
	controller.Groups.List = append(controller.Groups.List, &Group{Label: label})

	if err := controller.Groups.Write(controller.Database); err != nil {
		return 0, err
	}
	if err := controller.Groups.Read(controller.Database); err != nil {
		return 0, err
	}

	if group, ok := controller.Groups.GetGroup(label); ok {
		switch v := group.Id.(type) {
		case uint:
			return v, nil
		}
	}

	return 0, fmt.Errorf("unable to resolve group %q", label)
}

// resolveTagID looks up a tag by label, creating it if it does not exist.
// Returns the tag's _id.
func (rr *RR) resolveTagID(label string) (uint, error) {
	if label == "" {
		label = "Untagged"
	}

	controller := rr.Controller

	// Check in-memory list first
	if tag, ok := controller.Tags.GetTag(label); ok {
		switch v := tag.Id.(type) {
		case uint:
			return v, nil
		}
	}

	// Not found, create it
	controller.Tags.List = append(controller.Tags.List, &Tag{Label: label})

	if err := controller.Tags.Write(controller.Database); err != nil {
		return 0, err
	}
	if err := controller.Tags.Read(controller.Database); err != nil {
		return 0, err
	}

	if tag, ok := controller.Tags.GetTag(label); ok {
		switch v := tag.Id.(type) {
		case uint:
			return v, nil
		}
	}

	return 0, fmt.Errorf("unable to resolve tag %q", label)
}

// extractTrailingID extracts the portion of the URL path after the given prefix.
func extractTrailingID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimRight(id, "/")
	return id
}

// FrequenciesHandler handles GET /api/admin/rr/frequencies/{id}
func (rr *RR) FrequenciesHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/frequencies/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/frequencies/"+id)
}

// AgencyFreqsHandler handles GET /api/admin/rr/agency-freqs/{id}
func (rr *RR) AgencyFreqsHandler(w http.ResponseWriter, r *http.Request) {
	if !rr.requireAuth(w, r) {
		return
	}
	id := extractTrailingID(r.URL.Path, "/api/admin/rr/agency-freqs/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rr.proxySidecar(w, "/agency-freqs/"+id)
}

// RegisterRRHandlers registers all Radio Reference HTTP routes on the default mux.
func RegisterRRHandlers(controller *Controller) {
	log.Printf("RR_APP_KEY set: %v (len=%d)", DefaultRRAppKey != "", len(DefaultRRAppKey))
	rr := NewRR(controller)

	http.HandleFunc("/api/admin/rr/status", rr.StatusHandler)
	http.HandleFunc("/api/admin/rr/saved-creds", rr.SavedCredsHandler)
	http.HandleFunc("/api/admin/rr/clear-creds", rr.ClearCredsHandler)
	http.HandleFunc("/api/admin/rr/connect", rr.ConnectHandler)
	http.HandleFunc("/api/admin/rr/health", rr.HealthHandler)
	http.HandleFunc("/api/admin/rr/countries", rr.CountriesHandler)
	http.HandleFunc("/api/admin/rr/states/", rr.StatesHandler)
	http.HandleFunc("/api/admin/rr/counties/", rr.CountiesHandler)
	http.HandleFunc("/api/admin/rr/agencies/", rr.AgenciesHandler)
	http.HandleFunc("/api/admin/rr/systems/", rr.SystemsHandler)
	http.HandleFunc("/api/admin/rr/talkgroups/", rr.TalkgroupsHandler)
	http.HandleFunc("/api/admin/rr/frequencies/", rr.FrequenciesHandler)
	http.HandleFunc("/api/admin/rr/agency-freqs/", rr.AgencyFreqsHandler)
	http.HandleFunc("/api/admin/rr/updates/", rr.UpdatesHandler)
	http.HandleFunc("/api/admin/rr/import/", rr.ImportHandler)
}
