package modutui

func defaultBlockFromEntry(entry Entry) Block {
	return nodeGroupBlock{Marker: entryMarker(entry), Nodes: entry.Nodes}
}

func entryMarker(entry Entry) string {
	if entry.Plain {
		return ""
	}
	if entry.Role == RoleUser {
		return youStyle.Render("❯ ")
	}
	return assistantMarkerStyle.Render("● ")
}
