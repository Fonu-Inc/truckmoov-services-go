package dispatchhttp

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/truckmoov/services-go/internal/dispatch/application"
	"github.com/truckmoov/services-go/internal/dispatch/domain"
	httpx "github.com/truckmoov/services-go/pkg/http"
)

const maxAttachmentRequestBytes = 5 * 1024 * 1024

type dimensionsRequest struct {
	LengthCm float64 `json:"length_cm,omitempty"`
	WidthCm  float64 `json:"width_cm,omitempty"`
	HeightCm float64 `json:"height_cm,omitempty"`
}

type cargoRequest struct {
	Category        string             `json:"category,omitempty"`
	PackagesCount   int                `json:"packages_count,omitempty"`
	Unit            string             `json:"unit,omitempty"`
	ValueNgn        float64            `json:"value_ngn,omitempty"`
	Dimensions      *dimensionsRequest `json:"dimensions,omitempty"`
	SpecialHandling []string           `json:"special_handling,omitempty"`
	Equipment       []string           `json:"equipment,omitempty"`
}

func (c *cargoRequest) toDomain() domain.JobCargoDetails {
	if c == nil {
		return domain.JobCargoDetails{}
	}
	details := domain.JobCargoDetails{
		Category:        c.Category,
		PackagesCount:   c.PackagesCount,
		Unit:            c.Unit,
		ValueNgn:        c.ValueNgn,
		SpecialHandling: c.SpecialHandling,
		Equipment:       c.Equipment,
	}
	if c.Dimensions != nil {
		details.Dimensions = &domain.JobDimensions{LengthCm: c.Dimensions.LengthCm, WidthCm: c.Dimensions.WidthCm, HeightCm: c.Dimensions.HeightCm}
	}
	return details
}

func (c *cargoRequest) toDomainPtr() *domain.JobCargoDetails {
	if c == nil {
		return nil
	}
	details := c.toDomain()
	return &details
}

type driverInstructionOutcomeRequest struct {
	DriverID string `json:"driver_id"`
	Outcome  string `json:"outcome"`
	Channel  string `json:"channel,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// recordDriverInstructionOutcome is how the notification service reports
// whether the driver was really messaged, so the timeline only claims what
// happened.
func (h *Handler) recordDriverInstructionOutcome(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := httpx.ClaimsFromRequest(r)
	if !ok || claims == nil || claims.Service != "notification" || strings.TrimSpace(claims.TenantID) == "" {
		httpx.NotFound(w, r)
		return
	}
	tenantID := strings.TrimSpace(claims.TenantID)
	var req driverInstructionOutcomeRequest
	if err := decodeJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	job, err := h.jobLifecycleUseCase.RecordDriverInstructionOutcome(r.Context(), application.RecordDriverInstructionOutcomeCommand{
		TenantID: tenantID,
		JobID:    jobID,
		DriverID: req.DriverID,
		Outcome:  application.DriverInstructionOutcome(strings.TrimSpace(req.Outcome)),
		Channel:  firstNonEmptyDispatchValue(req.Channel, "whatsapp"),
		Reason:   req.Reason,
	})
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	h.respondJob(w, r, http.StatusOK, job)
}

type addJobAttachmentRequest struct {
	Name          string `json:"name"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type jobAttachmentContent struct {
	domain.JobAttachment
	ContentBase64 string `json:"content_base64"`
}

func (h *Handler) WithJobAttachments(useCase *application.JobAttachmentUseCase) *Handler {
	h.jobAttachmentUseCase = useCase
	return h
}

func (h *Handler) addJobAttachment(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.jobAttachmentUseCase == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "job attachments are not configured")
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentRequestBytes)
	var req addJobAttachmentRequest
	if err := decodeJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "name and base64 content are required")
		return
	}

	job, err := h.jobAttachmentUseCase.Add(r.Context(), application.AddJobAttachmentCommand{
		TenantID: tenantID, JobID: jobID, Name: req.Name, ContentType: req.ContentType, Data: data,
	})
	if err != nil {
		respondAttachmentError(w, err)
		return
	}
	h.respondJob(w, r, http.StatusCreated, job)
}

func (h *Handler) openJobAttachment(w http.ResponseWriter, r *http.Request, jobID, attachmentID string) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.jobAttachmentUseCase == nil || attachmentID == "" {
		httpx.WriteError(w, http.StatusNotFound, application.ErrAttachmentNotFound.Error())
		return
	}
	tenantID, ok := h.requireTenantID(w, r)
	if !ok {
		return
	}

	opened, err := h.jobAttachmentUseCase.Open(r.Context(), application.OpenJobAttachmentQuery{TenantID: tenantID, JobID: jobID, AttachmentID: attachmentID})
	if err != nil {
		respondAttachmentError(w, err)
		return
	}
	httpx.WriteOK(w, http.StatusOK, jobAttachmentContent{
		JobAttachment: opened.Attachment,
		ContentBase64: base64.StdEncoding.EncodeToString(opened.Data),
	})
}

func respondAttachmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrJobNotFound), errors.Is(err, application.ErrAttachmentNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, application.ErrAttachmentTooLarge):
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, application.ErrAttachmentEmpty), errors.Is(err, application.ErrTooManyAttachments):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		respondLifecycleError(w, err)
	}
}

func (h *Handler) resendDriverInstruction(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, authStatus, ok := authenticatedDispatcherTenantID(r)
	if !ok {
		httpx.WriteError(w, authStatus, http.StatusText(authStatus))
		return
	}

	job, err := h.jobLifecycleUseCase.ResendDriverInstruction(r.Context(), tenantID, jobID)
	if err != nil {
		respondLifecycleError(w, err)
		return
	}
	h.respondJob(w, r, http.StatusOK, job)
}
