package responsesmessages

import "encoding/json"

func appendMessagesTurn(messages *[]messagesMessage, message messagesMessage) {
	if len(*messages) == 0 || (*messages)[len(*messages)-1].Role != message.Role {
		*messages = append(*messages, message)
		return
	}
	var previous, current []messagesBlock
	_ = json.Unmarshal((*messages)[len(*messages)-1].Content, &previous)
	_ = json.Unmarshal(message.Content, &current)
	(*messages)[len(*messages)-1].Content = mustJSON(append(previous, current...))
}
