package domain

// MessageSendResult is what a provider reports about one send. ProviderMessageID
// is the id the provider knows the message by, which is how a later delivery
// receipt is matched back to it; it is empty when the provider issues none.
type MessageSendResult struct {
	Status            string
	ProviderMessageID string
}
