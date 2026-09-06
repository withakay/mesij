package cli

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const detailsMaxKeyWidth = 24

type detailsView struct {
	*tview.TextView
	fields []tuiField
	width  int
}

func newDetailsView() *detailsView {
	text := tview.NewTextView().
		SetScrollable(true).
		SetWrap(false).
		SetDynamicColors(true)
	text.SetBorder(true).SetTitle(" Details ")
	return &detailsView{TextView: text, width: -1}
}

func (d *detailsView) setFields(fields []tuiField) {
	if detailsFieldsEqual(d.fields, fields) {
		return
	}
	d.fields = append([]tuiField(nil), fields...)
	d.width = -1
}

func (d *detailsView) Draw(screen tcell.Screen) {
	_, _, width, _ := d.GetInnerRect()
	if width != d.width {
		d.width = width
		d.TextView.SetText(formatDetailsColumns(d.fields, width))
	}
	d.TextView.Draw(screen)
}

func detailsFieldsEqual(a, b []tuiField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key != b[i].key || a[i].value != b[i].value {
			return false
		}
	}
	return true
}

func formatDetailsColumns(fields []tuiField, width int) string {
	if len(fields) == 0 {
		return ""
	}
	if width < 1 {
		width = 1
	}
	keyWidth := detailsKeyColumnWidth(fields, width)
	gap := 1
	if keyWidth+gap >= width {
		gap = 0
		keyWidth = width / 2
		if keyWidth < 1 {
			keyWidth = 1
		}
	}
	valueWidth := width - keyWidth - gap
	if valueWidth < 1 {
		valueWidth = 1
		if keyWidth+gap+valueWidth > width {
			keyWidth = width - gap - valueWidth
			if keyWidth < 1 {
				keyWidth = 1
				gap = 0
				valueWidth = width - keyWidth
				if valueWidth < 1 {
					valueWidth = 1
				}
			}
		}
	}

	var b strings.Builder
	for _, field := range fields {
		keyLines := wrapDetailsText(tview.Escape(field.key), keyWidth)
		valueLines := wrapDetailsText(tview.Escape(field.value), valueWidth)
		n := len(keyLines)
		if len(valueLines) > n {
			n = len(valueLines)
		}
		for i := 0; i < n; i++ {
			key, value := "", ""
			if i < len(keyLines) {
				key = keyLines[i]
			}
			if i < len(valueLines) {
				value = valueLines[i]
			}
			pad := keyWidth - tview.TaggedStringWidth(key)
			if pad < 0 {
				pad = 0
			}
			b.WriteString(key)
			if pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
			if gap > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(value)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func detailsKeyColumnWidth(fields []tuiField, width int) int {
	maxKey := 0
	for _, field := range fields {
		w := tview.TaggedStringWidth(tview.Escape(field.key))
		if w > maxKey {
			maxKey = w
		}
	}
	capAt := detailsMaxKeyWidth
	if remain := width - 12; remain > 0 && remain < capAt {
		capAt = remain
	}
	if capAt < 8 {
		capAt = 8
	}
	if maxKey > capAt {
		maxKey = capAt
	}
	if maxKey < 1 {
		maxKey = 1
	}
	if maxKey >= width {
		maxKey = width / 2
		if maxKey < 1 {
			maxKey = 1
		}
	}
	return maxKey
}

func wrapDetailsText(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	parts := strings.Split(text, "\n")
	for len(parts) > 1 && parts[0] == "" {
		parts = parts[1:]
	}
	var lines []string
	for _, part := range parts {
		wrapped := tview.WordWrap(part, width)
		if len(wrapped) == 0 {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapped...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
