// Package handler implements HTTP request handlers for the pastebin service.
package handler

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pastebin/internal/auth"
	"pastebin/internal/idgen"
	"pastebin/internal/model"
	"pastebin/internal/ratelimit"
	"pastebin/internal/stats"
	"pastebin/internal/store"
)

const maxStaleBodyRead = 4096 // max bytes to discard from a body

// Handler processes HTTP requests for the pastebin service.
type Handler struct {
	store       *store.Store
	gen         *idgen.Generator
	cfg         *model.Config
	ttlRules    []model.TTLRule
	limiter     *ratelimit.Limiter
	reporter    *stats.Reporter
	helpText    string
	indexHTML   string
	logger      *slog.Logger
	requestSize int64 // max_paste_size + multipart_overhead
}

// New creates a new Handler.
func New(
	s *store.Store,
	g *idgen.Generator,
	cfg *model.Config,
	ttlRules []model.TTLRule,
	limiter *ratelimit.Limiter,
	reporter *stats.Reporter,
	helpText string,
	indexHTML string,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		store:       s,
		gen:         g,
		cfg:         cfg,
		ttlRules:    ttlRules,
		limiter:     limiter,
		reporter:    reporter,
		helpText:    helpText,
		indexHTML:   indexHTML,
		logger:      logger,
		requestSize: cfg.MaxPasteSize + cfg.MultipartOverhead,
	}
}

// Mux returns an http.Handler with all routes registered.
func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()

	// Exact paths
	mux.HandleFunc("GET /", h.handleGetRoot)
	mux.HandleFunc("POST /", h.handlePostRoot)

	// Parameterized paths
	mux.HandleFunc("GET /{id}", h.handleGetPaste)
	mux.HandleFunc("POST /{id}", h.handlePostPaste)
	mux.HandleFunc("DELETE /{id}", h.handleDelete)
	mux.HandleFunc("PUT /{id}", h.handlePut)

	return mux
}

// handleGetRoot handles GET / (help text, stats, or index page).
func (h *Handler) handleGetRoot(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)

	// Check for stats action
	if r.URL.Query().Get("action") == "stats" {
		h.handleStats(w, r, ip)
		return
	}

	// Browser requests: return index.html
	if strings.HasPrefix(r.UserAgent(), "Mozilla") && h.indexHTML != "" {
		respSize := int64(len(h.indexHTML))

		// Rate limit (read)
		if !h.limiter.Allow(ip, true, respSize, 0) {
			writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(h.indexHTML))
		return
	}

	// CLI / non-browser requests: return help text
	respSize := int64(len(h.helpText))

	// Rate limit (read)
	if !h.limiter.Allow(ip, true, respSize, 0) {
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(h.helpText))
}

// handleGetPaste handles GET /:id (retrieve paste content).
func (h *Handler) handleGetPaste(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)
	id := r.PathValue("id")

	p, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("get paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if p == nil {
		writeText(w, http.StatusNotFound, "not found")
		return
	}

	// Rate limit (read)
	respSize := int64(len(p.Content))
	if !h.limiter.Allow(ip, true, respSize, 0) {
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Conditional request support (304 Not Modified)
	lastModified := time.UnixMilli(p.UpdatedAt).UTC().Format(http.TimeFormat)
	w.Header().Set("Last-Modified", lastModified)
	w.Header().Set("Cache-Control", "public")

	if ifModSince := r.Header.Get("If-Modified-Since"); ifModSince != "" {
		if t, err := time.Parse(http.TimeFormat, ifModSince); err == nil {
			// Truncate both times to seconds for comparison (HTTP-date is second-precision)
			pasteTime := time.UnixMilli(p.UpdatedAt).UTC().Truncate(time.Second)
			if !pasteTime.After(t) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(p.Content)
}

// handlePostRoot handles POST / (create paste with auto-generated ID).
func (h *Handler) handlePostRoot(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)
	body, err := h.readBody(r, ip)
	if err != nil {
		writeText(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	// Generate ID with collision detection
	id, err := h.gen.Generate(func(candidate string) bool {
		exists, _ := h.store.Exists(candidate)
		return exists
	})
	if err != nil {
		h.logger.Error("id generation failed", "error", err)
		writeText(w, http.StatusServiceUnavailable, "service busy, try again")
		return
	}

	h.createPaste(w, r, id, body, ip)
}

// handlePostPaste handles POST /:id (create or overwrite a paste).
func (h *Handler) handlePostPaste(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)
	id := r.PathValue("id")

	// Check if paste exists and is locked
	existing, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("get existing paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if existing != nil && existing.Locked {
		// Locked: requires admin
		if !auth.IsAdmin(r, h.cfg.AdminKeyHash) {
			writeText(w, http.StatusForbidden, "paste is locked, admin required")
			return
		}
	}

	body, err := h.readBody(r, ip)
	if err != nil {
		writeText(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	h.createPaste(w, r, id, body, ip)
}

// handleDelete handles DELETE /:id.
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)
	id := r.PathValue("id")

	// Check if paste exists and is locked
	existing, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("get paste for delete", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if existing == nil {
		writeText(w, http.StatusNotFound, "not found")
		return
	}

	// Locked: requires admin
	if existing.Locked && !auth.IsAdmin(r, h.cfg.AdminKeyHash) {
		writeText(w, http.StatusForbidden, "paste is locked, admin required")
		return
	}

	// Rate limit (write)
	usage := -existing.Size // negative because we're removing usage
	if !h.limiter.Allow(ip, false, 0, usage) {
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	if err := h.store.Delete(id); err != nil {
		h.logger.Error("delete paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.logger.Info("paste deleted", "id", id, "ip", ip)
	writeText(w, http.StatusOK, "deleted")
}

// handlePut handles PUT /:id?action=lock|unlock (admin only).
func (h *Handler) handlePut(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, h.cfg.BehindProxy)
	id := r.PathValue("id")

	if !auth.IsAdmin(r, h.cfg.AdminKeyHash) {
		writeText(w, http.StatusUnauthorized, "admin required")
		return
	}

	action := r.URL.Query().Get("action")
	switch action {
	case "lock":
		h.lockPaste(w, r, id, ip)
	case "unlock":
		h.unlockPaste(w, r, id, ip)
	default:
		writeText(w, http.StatusBadRequest, "invalid action, use lock or unlock")
	}
}

// lockPaste locks a paste (admin only).
func (h *Handler) lockPaste(w http.ResponseWriter, r *http.Request, id, ip string) {
	existing, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("get paste for lock", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if existing == nil {
		writeText(w, http.StatusNotFound, "not found")
		return
	}
	if existing.Locked {
		writeText(w, http.StatusOK, "already locked")
		return
	}

	now := time.Now().UnixMilli()
	if err := h.store.Lock(id, ip, now); err != nil {
		h.logger.Error("lock paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.logger.Info("paste locked", "id", id, "ip", ip)
	writeText(w, http.StatusOK, "locked")
}

// unlockPaste unlocks a paste (admin only).
func (h *Handler) unlockPaste(w http.ResponseWriter, r *http.Request, id, ip string) {
	existing, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("get paste for unlock", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if existing == nil {
		writeText(w, http.StatusNotFound, "not found")
		return
	}
	if !existing.Locked {
		writeText(w, http.StatusOK, "already unlocked")
		return
	}

	now := time.Now().UnixMilli()
	ttl := ttlForLength(h.ttlRules, len(id))
	expiredAt := now + ttl.Milliseconds()

	if err := h.store.Unlock(id, expiredAt, ip, now); err != nil {
		h.logger.Error("unlock paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.logger.Info("paste unlocked", "id", id, "ip", ip)
	writeText(w, http.StatusOK, "unlocked")
}

// handleStats responds with cached or freshly generated statistics.
func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request, ip string) {
	isAdmin := auth.IsAdmin(r, h.cfg.AdminKeyHash)

	var s *model.Stats
	if isAdmin {
		s = h.reporter.GenerateNow()
	} else {
		s = h.reporter.GetCached()
		if s == nil {
			writeText(w, http.StatusServiceUnavailable, "stats not available yet")
			return
		}
	}

	// Rate limit (read) - approximate size
	respSize := int64(512) // rough estimate
	if !h.limiter.Allow(ip, true, respSize, 0) {
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Build a simple text response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("db_size: %d\n", s.DBSize))
	sb.WriteString(fmt.Sprintf("paste_count: %d\n", s.PasteCount))
	sb.WriteString(fmt.Sprintf("mem_alloc: %d\n", s.MemAlloc))
	sb.WriteString(fmt.Sprintf("mem_sys: %d\n", s.MemSys))
	sb.WriteString(fmt.Sprintf("num_gc: %d\n", s.NumGC))
	sb.WriteString(fmt.Sprintf("generated_at: %d\n", s.GeneratedAt))
	sb.WriteString(fmt.Sprintf("generated_at_human: %s\n", time.UnixMilli(s.GeneratedAt).UTC().Format(time.RFC3339)))
	sb.WriteString("recent_pastes:\n")
	for _, rp := range s.RecentPastes {
		sb.WriteString(fmt.Sprintf("  - %s (updated: %d, %s)\n",
			rp.ID, rp.UpdatedAt, time.UnixMilli(rp.UpdatedAt).UTC().Format(time.RFC3339)))
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(sb.String()))
}

// createPaste saves a paste and responds with its URL.
func (h *Handler) createPaste(w http.ResponseWriter, r *http.Request, id string, body []byte, ip string) {
	now := time.Now().UnixMilli()

	// Determine TTL based on ID length
	ttl := ttlForLength(h.ttlRules, len(id))
	expiredAt := now + ttl.Milliseconds()

	// Get old paste size for usage tracking
	oldSize, _ := h.store.GetOldSize(id)
	usage := int64(len(body)) - oldSize

	// Check DB size before writing
	dbSize, err := h.store.DBSize()
	if err != nil {
		h.logger.Error("db size check", "error", err)
	}
	if dbSize+int64(len(body))-oldSize > h.cfg.DBMaxSize {
		writeText(w, http.StatusServiceUnavailable, "database full")
		return
	}

	// Rate limit (write)
	if !h.limiter.Allow(ip, false, int64(len(body)), usage) {
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Check if it's a new paste or overwrite
	createdAt := now
	createdBy := ip

	existing, err := h.store.Get(id)
	if err != nil {
		h.logger.Error("check existing paste for create", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if existing != nil {
		// Overwriting: preserve original created_at and created_by
		createdAt = existing.CreatedAt
		createdBy = existing.CreatedBy
	}

	p := &model.Paste{
		ID:        id,
		Content:   body,
		Size:      int64(len(body)),
		Locked:    false,
		CreatedAt: createdAt,
		UpdatedAt: now,
		ExpiredAt: expiredAt,
		CreatedBy: createdBy,
		UpdatedBy: ip,
	}

	if err := h.store.Set(p); err != nil {
		h.logger.Error("save paste", "id", id, "error", err)
		writeText(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h.logger.Info("paste created", "id", id, "size", len(body), "ip", ip)

	// Build the full URL for the response
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h.cfg.BehindProxy {
		if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
			host = fwdHost
		}
		if fwdProto := r.Header.Get("X-Forwarded-Proto"); fwdProto != "" {
			scheme = fwdProto
		}
	}

	url := fmt.Sprintf("%s://%s/%s", scheme, host, id)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(url + "\n"))
}

// readBody reads the request body, supporting both raw and multipart/form-data.
func (h *Handler) readBody(r *http.Request, ip string) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, h.requestSize)
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")

	// Try multipart form data
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return h.readMultipart(r)
	}

	// Read raw body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// readMultipart reads the "file" field from a multipart form body.
func (h *Handler) readMultipart(r *http.Request) ([]byte, error) {
	// We need to re-wrap the body for the multipart reader because
	// MaxBytesReader has already been applied. We use the existing r.Body.
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("multipart reader: %w", err)
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("multipart next part: %w", err)
		}

		if part.FormName() == "file" {
			body, err := io.ReadAll(part)
			part.Close()
			return body, err
		}
		part.Close()
	}

	// No "file" field found, return empty
	return nil, nil
}

// writeText writes a plain text response with the given status code.
func writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(msg + "\n"))
}
