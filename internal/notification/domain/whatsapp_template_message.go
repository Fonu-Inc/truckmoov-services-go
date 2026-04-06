package domain

type WhatsAppTemplateMessageRequest struct {
	Recipient  string           `json:"recipient"`
	Name       string           `json:"name"`
	Language   string           `json:"language"`
	Components []map[string]any `json:"components,omitempty"`
}
