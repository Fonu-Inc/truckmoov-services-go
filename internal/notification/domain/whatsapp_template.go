package domain

type WhatsAppTemplateCreateRequest struct {
	Name                string           `json:"name"`
	Language            string           `json:"language"`
	Category            string           `json:"category"`
	AllowCategoryChange *bool            `json:"allow_category_change,omitempty"`
	ParameterFormat     string           `json:"parameter_format,omitempty"`
	Components          []map[string]any `json:"components"`
}

type WhatsAppTemplateCreateResponse struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Category string `json:"category,omitempty"`
}

type WhatsAppTemplate struct {
	ID               string           `json:"id,omitempty"`
	Name             string           `json:"name,omitempty"`
	Language         string           `json:"language,omitempty"`
	Status           string           `json:"status,omitempty"`
	Category         string           `json:"category,omitempty"`
	SubCategory      string           `json:"sub_category,omitempty"`
	PreviousCategory string           `json:"previous_category,omitempty"`
	RejectedReason   string           `json:"rejected_reason,omitempty"`
	Components       []map[string]any `json:"components,omitempty"`
}

type WhatsAppTemplateListRequest struct {
	Limit int    `json:"limit,omitempty"`
	After string `json:"after,omitempty"`
}

type WhatsAppTemplatePaging struct {
	Cursors map[string]string `json:"cursors,omitempty"`
	Next    string            `json:"next,omitempty"`
}

type WhatsAppTemplateListResponse struct {
	Data   []WhatsAppTemplate      `json:"data"`
	Paging *WhatsAppTemplatePaging `json:"paging,omitempty"`
}
