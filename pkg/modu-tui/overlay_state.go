package modutui

func (o *overlayModel) reset() {
	*o = overlayModel{}
}

func (o *overlayModel) openPanel(panel Panel) {
	o.reset()
	o.panel = &panel
	o.panelSelected = clamp(panel.Selected, 0, max(0, len(panel.Rows)-1))
}

func (o *overlayModel) refreshPanel(panel Panel) {
	if o.panel == nil || o.panel.ID != panel.ID {
		o.openPanel(panel)
		return
	}
	selected := preserveRowSelection(o.panel.Rows, o.panelSelected, panel.Rows)
	offset := o.panelOffset
	o.panel = &panel
	o.panelSelected = selected
	o.panelOffset = offset
}

func (o *overlayModel) closePanel(id string) bool {
	if o.panel == nil || (id != "" && id != o.panel.ID) {
		return false
	}
	o.reset()
	return true
}

func (o *overlayModel) openApproval(approval pendingApproval) {
	o.reset()
	o.approval = &approval
}

func (o *overlayModel) cancelApproval(id string) bool {
	if o.approval == nil || (id != "" && id != o.approval.request.ID) {
		return false
	}
	o.approval = nil
	return true
}

func (o *overlayModel) openHumanPrompt(prompt pendingHumanPrompt) {
	o.reset()
	o.humanPrompt = &prompt
}

func (o *overlayModel) cancelHumanPrompt(id string) bool {
	if o.humanPrompt == nil || (id != "" && id != o.humanPrompt.request.ID) {
		return false
	}
	o.humanPrompt = nil
	return true
}

func (o *overlayModel) openHumanText(prompt pendingHumanText) {
	o.reset()
	o.humanText = &prompt
}

func (o *overlayModel) cancelHumanText(id string) bool {
	if o.humanText == nil || (id != "" && id != o.humanText.request.ID) {
		return false
	}
	o.humanText = nil
	return true
}
