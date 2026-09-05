package chatresponses

import "encoding/json"

type approximateLocation struct {
	City     string `json:"city,omitempty"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

func chatWebSearchToResponses(raw json.RawMessage) (*responsesTool, error) {
	if !nonNullJSON(raw) {
		return nil, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, "$.web_search_options", raw, "search_context_size", "user_location")
	if err != nil {
		return nil, err
	}
	tool := &responsesTool{Type: "web_search"}
	if nonNullJSON(fields["search_context_size"]) {
		if err := json.Unmarshal(fields["search_context_size"], &tool.SearchContextSize); err != nil || !validSearchContextSize(tool.SearchContextSize) {
			return nil, invalid(ProtocolChat, "$.web_search_options.search_context_size", "must be low, medium, or high")
		}
	}
	if nonNullJSON(fields["user_location"]) {
		locationFields, err := rejectUnknownObjectFields(ProtocolChat, "$.web_search_options.user_location", fields["user_location"], "type", "approximate")
		if err != nil {
			return nil, err
		}
		if rawString(locationFields["type"]) != "approximate" {
			return nil, invalid(ProtocolChat, "$.web_search_options.user_location.type", "must be %q", "approximate")
		}
		location, err := decodeApproximateLocation(ProtocolChat, "$.web_search_options.user_location.approximate", locationFields["approximate"])
		if err != nil {
			return nil, err
		}
		converted := map[string]any{"type": "approximate"}
		for name, value := range map[string]string{
			"city": location.City, "country": location.Country, "region": location.Region, "timezone": location.Timezone,
		} {
			if value != "" {
				converted[name] = value
			}
		}
		tool.UserLocation = mustJSON(converted)
	}
	return tool, nil
}

func responsesWebSearchToChat(tool responsesTool, path string) (json.RawMessage, error) {
	if tool.Type != "web_search" {
		return nil, unsupported(ProtocolResponses, path+".type", "versioned web-search tool %q has no exact Chat equivalent", tool.Type)
	}
	if tool.ExternalWebAccess != nil && !*tool.ExternalWebAccess {
		return nil, unsupported(ProtocolResponses, path+".external_web_access", "Chat cannot request cache-only web search")
	}
	if nonNullJSON(tool.Filters) {
		fields, err := rejectUnknownObjectFields(ProtocolResponses, path+".filters", tool.Filters, "allowed_domains")
		if err != nil {
			return nil, err
		}
		var domains []string
		if nonNullJSON(fields["allowed_domains"]) {
			if err := json.Unmarshal(fields["allowed_domains"], &domains); err != nil {
				return nil, invalid(ProtocolResponses, path+".filters.allowed_domains", "must be a string array")
			}
		}
		if len(domains) > 0 {
			return nil, unsupported(ProtocolResponses, path+".filters.allowed_domains", "Chat web search has no domain filter")
		}
	}
	if tool.SearchContextSize != "" && !validSearchContextSize(tool.SearchContextSize) {
		return nil, invalid(ProtocolResponses, path+".search_context_size", "must be low, medium, or high")
	}
	options := map[string]any{}
	if tool.SearchContextSize != "" {
		options["search_context_size"] = tool.SearchContextSize
	}
	if nonNullJSON(tool.UserLocation) {
		fields, err := rejectUnknownObjectFields(ProtocolResponses, path+".user_location", tool.UserLocation, "type", "city", "country", "region", "timezone")
		if err != nil {
			return nil, err
		}
		locationType := rawString(fields["type"])
		if locationType != "" && locationType != "approximate" {
			return nil, invalid(ProtocolResponses, path+".user_location.type", "must be %q", "approximate")
		}
		location, err := decodeApproximateLocationFields(ProtocolResponses, path+".user_location", fields)
		if err != nil {
			return nil, err
		}
		options["user_location"] = map[string]any{"type": "approximate", "approximate": location}
	}
	return mustJSON(options), nil
}

func decodeApproximateLocation(protocol Protocol, path string, raw json.RawMessage) (approximateLocation, error) {
	if !nonNullJSON(raw) {
		return approximateLocation{}, invalid(protocol, path, "is required")
	}
	fields, err := rejectUnknownObjectFields(protocol, path, raw, "city", "country", "region", "timezone")
	if err != nil {
		return approximateLocation{}, err
	}
	return decodeApproximateLocationFields(protocol, path, fields)
}

func decodeApproximateLocationFields(protocol Protocol, path string, fields map[string]json.RawMessage) (approximateLocation, error) {
	var location approximateLocation
	for name, destination := range map[string]*string{
		"city": &location.City, "country": &location.Country, "region": &location.Region, "timezone": &location.Timezone,
	} {
		if nonNullJSON(fields[name]) {
			if err := json.Unmarshal(fields[name], destination); err != nil {
				return approximateLocation{}, invalid(protocol, path+"."+name, "must be a string")
			}
		}
	}
	return location, nil
}

func validSearchContextSize(value string) bool {
	return value == "low" || value == "medium" || value == "high"
}
