package domain

type WhatsAppSendResult struct {
	Status    string `json:"status,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

type WhatsAppMediaMessageRequest struct {
	Recipient string `json:"recipient"`
	MediaType string `json:"media_type"`
	MediaID   string `json:"media_id,omitempty"`
	MediaLink string `json:"media_link,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Filename  string `json:"filename,omitempty"`
}
