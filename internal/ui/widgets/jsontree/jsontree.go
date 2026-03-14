//go:build darwin || linux || windows

package jsontree

import (
	"encoding/json"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const rootID = "root"

// SearchableJSONTree renders JSON as a tree with a built-in search bar.
type SearchableJSONTree struct {
	rootID string

	treeNodes    map[string]*jsonTreeNode
	treeVisible  map[string]bool
	treeMatches  map[string]bool
	treeQuery    string
	searchIndex  map[string][]string
	searchAllIDs []string

	matchIDs   []string
	matchIndex int
	selectedID string

	tree   *widget.Tree
	scroll *container.Scroll

	searchEntry *escEntry
	searchUp    *widget.Button
	searchDown  *widget.Button
	searchWrap  *fyne.Container
	searchWidth float32

	debounceMu    sync.Mutex
	debounceTimer *time.Timer
	debounceQuery string
}

type jsonTreeNode struct {
	key        string
	value      string
	children   []string
	parent     string
	searchText string
	tokens     []string
	height     int
}

// NewSearchableJSONTree creates a JSON tree widget with an embedded search bar.
func NewSearchableJSONTree() *SearchableJSONTree {
	jt := &SearchableJSONTree{rootID: rootID}

	jt.tree = widget.NewTree(
		jt.childUIDs,
		jt.isBranch,
		func(branch bool) fyne.CanvasObject {
			makeText := func() *canvas.Text {
				lbl := canvas.NewText("", theme.ForegroundColor())
				lbl.TextSize = theme.TextSize() - 1
				lbl.TextStyle = fyne.TextStyle{Monospace: true}
				return lbl
			}
			return container.NewHBox(makeText(), makeText(), makeText())
		},
		jt.updateNode,
	)
	jt.tree.OnSelected = func(uid string) {
		jt.selectedID = uid
	}
	jt.tree.OnUnselected = func(uid string) {
		if jt.selectedID == uid {
			jt.selectedID = ""
		}
	}

	jt.tree.Root = jt.rootID
	jt.tree.HideSeparators = true
	jt.scroll = container.NewScroll(jt.tree)

	jt.searchEntry = newEscEntry()
	jt.searchEntry.SetPlaceHolder("Search output")
	jt.searchEntry.OnChanged = jt.onSearchChanged
	jt.searchEntry.OnSubmitted = func(_ string) {
		jt.navigateMatch(1)
	}
	jt.searchEntry.SetOnEsc(func() {
		if jt.SearchVisible() {
			jt.SetSearchVisible(false)
		}
	})

	jt.searchUp = widget.NewButton("▲", func() {
		jt.navigateMatch(-1)
	})
	jt.searchDown = widget.NewButton("▼", func() {
		jt.navigateMatch(1)
	})
	jt.searchUp.Disable()
	jt.searchDown.Disable()

	jt.searchWrap = container.NewGridWrap(
		fyne.NewSize(420, jt.searchEntry.MinSize().Height),
		container.NewHBox(jt.searchEntry, jt.searchUp, jt.searchDown),
	)
	jt.searchWidth = 420
	jt.searchWrap.Hide()

	return jt
}

// View returns the scrollable tree view.
func (jt *SearchableJSONTree) View() fyne.CanvasObject {
	return jt.scroll
}

// SearchBar returns the search UI container.
func (jt *SearchableJSONTree) SearchBar() fyne.CanvasObject {
	return jt.searchWrap
}

// SearchEntry exposes the search input for focus management.
func (jt *SearchableJSONTree) SearchEntry() *widget.Entry {
	return &jt.searchEntry.Entry
}

// SetSearchWidth sets a fixed width for the search input wrapper.
func (jt *SearchableJSONTree) SetSearchWidth(w float32) {
	if w <= 0 {
		return
	}
	jt.searchWidth = w
	btnW := jt.searchUp.MinSize().Width + jt.searchDown.MinSize().Width + theme.Padding()*2
	entryW := w - btnW
	if entryW < jt.searchEntry.MinSize().Width {
		entryW = jt.searchEntry.MinSize().Width
	}

	entryWrap := container.NewGridWrap(
		fyne.NewSize(entryW, jt.searchEntry.MinSize().Height),
		jt.searchEntry,
	)
	row := container.NewHBox(entryWrap, jt.searchUp, jt.searchDown)
	row.Resize(fyne.NewSize(w, jt.searchEntry.MinSize().Height))
	row.Refresh()

	jt.searchWrap.Objects = []fyne.CanvasObject{row}
	jt.searchWrap.Resize(fyne.NewSize(w, jt.searchEntry.MinSize().Height))
	jt.searchWrap.Refresh()
}

// SetSearchVisible shows or hides the search bar and clears query when hidden.
func (jt *SearchableJSONTree) SetSearchVisible(show bool) {
	if show {
		jt.SetSearchWidth(jt.searchWidth)
		jt.searchWrap.Show()
		jt.searchWrap.Refresh()
		return
	}
	jt.searchEntry.SetText("")
	jt.applyTreeFilter("")
	jt.searchWrap.Hide()
	jt.searchWrap.Refresh()
}

// SearchVisible reports whether the search bar is visible.
func (jt *SearchableJSONTree) SearchVisible() bool {
	return jt.searchWrap.Visible()
}

// SetJSON rebuilds the tree from JSON text.
func (jt *SearchableJSONTree) SetJSON(jsonText string) {
	jt.treeNodes = make(map[string]*jsonTreeNode)
	jt.treeVisible = nil
	jt.treeMatches = nil
	jt.treeQuery = ""
	jt.matchIDs = nil
	jt.matchIndex = -1
	jt.SetSearchVisible(false)

	if strings.TrimSpace(jsonText) == "" {
		jt.tree.Refresh()
		return
	}

	var v any
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		jt.buildTree(jt.rootID, "root", "", map[string]any{"error": "invalid JSON"})
	} else {
		jt.buildTree(jt.rootID, "root", "", v)
	}
	jt.buildSearchIndex()
	jt.tree.Refresh()
	jt.tree.OpenBranch(jt.rootID)
}

func (jt *SearchableJSONTree) childUIDs(uid string) []string {
	if jt.treeNodes == nil {
		return nil
	}
	n, ok := jt.treeNodes[uid]
	if !ok {
		return nil
	}
	if jt.treeQuery == "" || jt.treeVisible == nil {
		return n.children
	}
	kids := make([]string, 0, len(n.children))
	for _, c := range n.children {
		if jt.treeVisible[c] {
			kids = append(kids, c)
		}
	}
	return kids
}

func (jt *SearchableJSONTree) isBranch(uid string) bool {
	if jt.treeNodes == nil {
		return false
	}
	n, ok := jt.treeNodes[uid]
	if !ok {
		return false
	}
	if jt.treeQuery == "" || jt.treeVisible == nil {
		return len(n.children) > 0
	}
	for _, c := range n.children {
		if jt.treeVisible[c] {
			return true
		}
	}
	return false
}

func (jt *SearchableJSONTree) updateNode(uid string, branch bool, obj fyne.CanvasObject) {
	row := obj.(*fyne.Container)
	if len(row.Objects) < 3 {
		return
	}
	keyText := row.Objects[0].(*canvas.Text)
	sepText := row.Objects[1].(*canvas.Text)
	valText := row.Objects[2].(*canvas.Text)

	if uid == jt.rootID || jt.treeNodes == nil {
		keyText.Text = ""
		sepText.Text = ""
		valText.Text = ""
		sepText.Hide()
		valText.Hide()
		keyText.Color = theme.ForegroundColor()
		sepText.Color = theme.ForegroundColor()
		valText.Color = theme.ForegroundColor()
		keyText.Refresh()
		sepText.Refresh()
		valText.Refresh()
		return
	}

	n, ok := jt.treeNodes[uid]
	if !ok {
		keyText.Text = ""
		sepText.Text = ""
		valText.Text = ""
		sepText.Hide()
		valText.Hide()
		keyText.Color = theme.ForegroundColor()
		sepText.Color = theme.ForegroundColor()
		valText.Color = theme.ForegroundColor()
		keyText.Refresh()
		sepText.Refresh()
		valText.Refresh()
		return
	}

	value := n.value
	if len(n.children) > 0 && jt.tree.IsBranchOpen(uid) && value == "{...}" {
		value = ""
	}

	keyText.Text = n.key
	if value == "" {
		sepText.Text = ""
		valText.Text = ""
		sepText.Hide()
		valText.Hide()
	} else {
		sepText.Text = ": "
		valText.Text = value
		sepText.Show()
		valText.Show()
	}

	if jt.treeMatches != nil && jt.treeMatches[uid] {
		keyText.Color = theme.PrimaryColor()
		sepText.Color = theme.PrimaryColor()
		valText.Color = theme.PrimaryColor()
	} else {
		keyText.Color = jsonKeyColor()
		sepText.Color = jsonPunctColor()
		valText.Color = jsonValueColor(value)
	}

	keyText.Refresh()
	sepText.Refresh()
	valText.Refresh()
}

func (jt *SearchableJSONTree) buildTree(id, key, parent string, v any) {
	n := &jsonTreeNode{key: key, parent: parent}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			n.value = "{}"
		} else {
			n.value = "{...}"
		}
		for _, k := range keys {
			childID := id + "." + k
			n.children = append(n.children, childID)
			jt.buildTree(childID, k, id, t[k])
		}
	case []any:
		n.value = fmt.Sprintf("[%d]", len(t))
		for i, item := range t {
			childKey := fmt.Sprintf("[%d]", i)
			childID := fmt.Sprintf("%s[%d]", id, i)
			n.children = append(n.children, childID)
			jt.buildTree(childID, childKey, id, item)
		}
	default:
		n.value = formatScalar(t)
	}
	maxH := -1
	for _, c := range n.children {
		cn := jt.treeNodes[c]
		if cn == nil {
			continue
		}
		if cn.height > maxH {
			maxH = cn.height
		}
	}
	if maxH < 0 {
		n.height = 0
	} else {
		n.height = maxH + 1
	}

	n.searchText = strings.ToLower(strings.TrimSpace(n.key + " " + n.value))
	n.tokens = tokenizeQuery(n.searchText)
	jt.treeNodes[id] = n
}

func (jt *SearchableJSONTree) buildSearchIndex() {
	idx := make(map[string][]string, len(jt.treeNodes))
	all := make([]string, 0, len(jt.treeNodes))
	for id, n := range jt.treeNodes {
		all = append(all, id)
		if n == nil || n.searchText == "" {
			continue
		}
		seen := make(map[string]struct{})
		for _, tok := range n.tokens {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			idx[tok] = append(idx[tok], id)

			if len(tok) >= 2 {
				maxPref := 3
				if len(tok) < maxPref {
					maxPref = len(tok)
				}
				for l := 2; l <= maxPref; l++ {
					pref := tok[:l]
					if _, ok := seen[pref]; ok {
						continue
					}
					seen[pref] = struct{}{}
					idx[pref] = append(idx[pref], id)
				}
			}
		}
	}
	jt.searchIndex = idx
	jt.searchAllIDs = all
}

func (jt *SearchableJSONTree) applyTreeFilter(q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	jt.treeQuery = q
	if q == "" {
		jt.treeVisible = nil
		jt.treeMatches = nil
		jt.matchIDs = nil
		jt.matchIndex = -1
		jt.updateNavButtons()
		jt.tree.Refresh()
		return
	}
	vis := make(map[string]bool)
	match := make(map[string]bool)
	matchedContainers := make(map[string]bool)

	markDescendants := func(start string) {
		stack := []string{start}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if !vis[id] {
				vis[id] = true
			}

			n := jt.treeNodes[id]
			if n == nil {
				continue
			}
			stack = append(stack, n.children...)
		}
	}

	markPath := func(id string) {
		cur := id
		for cur != "" {
			vis[cur] = true
			n := jt.treeNodes[cur]
			if n == nil {
				break
			}
			cur = n.parent
		}
	}

	selectContainer := func(id string) string {
		n := jt.treeNodes[id]
		if n == nil {
			return id
		}
		if len(n.children) > 0 {
			return id
		}
		if n.parent != "" {
			return n.parent
		}
		return id
	}

	candidates := jt.searchAllIDs
	qTokens := tokenizeQuery(q)
	if len(qTokens) > 0 && jt.searchIndex != nil {
		cur := jt.searchIndex[qTokens[0]]
		for _, tok := range qTokens[1:] {
			next := jt.searchIndex[tok]
			if len(cur) == 0 || len(next) == 0 {
				cur = nil
				break
			}
			set := make(map[string]struct{}, len(cur))
			for _, id := range cur {
				set[id] = struct{}{}
			}
			inter := make([]string, 0, len(next))
			for _, id := range next {
				if _, ok := set[id]; ok {
					inter = append(inter, id)
				}
			}
			cur = inter
		}
		if cur != nil {
			candidates = cur
		}
	}

	jt.matchIDs = jt.matchIDs[:0]
	for _, id := range candidates {
		n := jt.treeNodes[id]
		if n == nil {
			continue
		}
		if !matchNode(n, q, qTokens) {
			continue
		}
		jt.matchIDs = append(jt.matchIDs, id)
		match[id] = true
		markPath(id)

		containerID := selectContainer(id)
		matchedContainers[containerID] = true
	}

	for id := range matchedContainers {
		markDescendants(id)
	}

	visibleIDs := make([]string, 0, len(vis))
	for id := range vis {
		visibleIDs = append(visibleIDs, id)
	}
	for _, id := range visibleIDs {
		n := jt.treeNodes[id]
		if n == nil {
			continue
		}
		if n.height <= 2 {
			markDescendants(id)
		}
	}

	sort.Strings(jt.matchIDs)
	if len(jt.matchIDs) == 0 {
		jt.matchIndex = -1
	} else if jt.matchIndex < 0 || jt.matchIndex >= len(jt.matchIDs) {
		jt.matchIndex = 0
	}
	jt.updateNavButtons()
	jt.treeVisible = vis
	jt.treeMatches = match
	jt.tree.Refresh()
}

func (jt *SearchableJSONTree) onSearchChanged(s string) {
	jt.debounceMu.Lock()
	jt.debounceQuery = s
	if jt.debounceTimer == nil {
		jt.debounceTimer = time.AfterFunc(300*time.Millisecond, jt.fireSearchDebounce)
		jt.debounceMu.Unlock()
		return
	}
	if !jt.debounceTimer.Stop() {
		select {
		case <-jt.debounceTimer.C:
		default:
		}
	}
	jt.debounceTimer.Reset(300 * time.Millisecond)
	jt.debounceMu.Unlock()
}

func (jt *SearchableJSONTree) fireSearchDebounce() {
	jt.debounceMu.Lock()
	q := jt.debounceQuery
	jt.debounceMu.Unlock()
	fyne.Do(func() {
		if jt.treeNodes == nil {
			return
		}
		jt.applyTreeFilter(q)
	})
}

func (jt *SearchableJSONTree) navigateMatch(step int) {
	if len(jt.matchIDs) == 0 {
		return
	}
	jt.matchIndex += step
	if jt.matchIndex < 0 {
		jt.matchIndex = len(jt.matchIDs) - 1
	} else if jt.matchIndex >= len(jt.matchIDs) {
		jt.matchIndex = 0
	}
	id := jt.matchIDs[jt.matchIndex]
	jt.openPath(id)
	jt.tree.Select(id)
	jt.tree.ScrollTo(id)
}

func (jt *SearchableJSONTree) openPath(id string) {
	cur := id
	for cur != "" {
		jt.tree.OpenBranch(cur)
		n := jt.treeNodes[cur]
		if n == nil {
			break
		}
		cur = n.parent
	}
}

func (jt *SearchableJSONTree) updateNavButtons() {
	if jt.searchUp == nil || jt.searchDown == nil {
		return
	}
	if len(jt.matchIDs) == 0 {
		jt.searchUp.Disable()
		jt.searchDown.Disable()
		return
	}
	jt.searchUp.Enable()
	jt.searchDown.Enable()
}

// SelectedValueString returns a JSON string for the selected node value.
func (jt *SearchableJSONTree) SelectedValueString() string {
	id := jt.selectedID
	if id == "" || jt.treeNodes == nil {
		return ""
	}
	n := jt.treeNodes[id]
	if n == nil {
		return ""
	}
	if len(n.children) == 0 {
		if n.value == "" {
			return ""
		}
		return n.value
	}
	val := jt.nodeToValue(id)
	b, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func (jt *SearchableJSONTree) nodeToValue(id string) any {
	n := jt.treeNodes[id]
	if n == nil {
		return nil
	}
	if len(n.children) == 0 {
		switch n.value {
		case "{}":
			return map[string]any{}
		case "null":
			return nil
		}
		if strings.HasPrefix(n.value, "[") && strings.HasSuffix(n.value, "]") {
			return []any{}
		}
		var v any
		if err := json.Unmarshal([]byte(n.value), &v); err == nil {
			return v
		}
		return n.value
	}

	isArray := true
	maxIdx := -1
	idxMap := make(map[int]any, len(n.children))
	for _, cid := range n.children {
		cn := jt.treeNodes[cid]
		if cn == nil {
			continue
		}
		idx, ok := parseArrayIndex(cn.key)
		if !ok {
			isArray = false
			break
		}
		if idx > maxIdx {
			maxIdx = idx
		}
		idxMap[idx] = jt.nodeToValue(cid)
	}
	if isArray {
		if maxIdx < 0 {
			return []any{}
		}
		arr := make([]any, maxIdx+1)
		for i := 0; i <= maxIdx; i++ {
			arr[i] = idxMap[i]
		}
		return arr
	}

	obj := make(map[string]any, len(n.children))
	for _, cid := range n.children {
		cn := jt.treeNodes[cid]
		if cn == nil {
			continue
		}
		obj[cn.key] = jt.nodeToValue(cid)
	}
	return obj
}

func parseArrayIndex(key string) (int, bool) {
	if len(key) < 3 || key[0] != '[' || key[len(key)-1] != ']' {
		return 0, false
	}
	i, err := strconv.Atoi(key[1 : len(key)-1])
	if err != nil || i < 0 {
		return 0, false
	}
	return i, true
}

func jsonKeyColor() color.Color {
	return color.NRGBA{R: 0x8B, G: 0xC4, B: 0xF9, A: 0xFF}
}

func jsonStringColor() color.Color {
	return color.NRGBA{R: 0x9E, G: 0xD9, B: 0x8A, A: 0xFF}
}

func jsonNumberColor() color.Color {
	return color.NRGBA{R: 0xF2, G: 0x9D, B: 0x50, A: 0xFF}
}

func jsonBoolColor() color.Color {
	return color.NRGBA{R: 0xB3, G: 0x8D, B: 0xF7, A: 0xFF}
}

func jsonNullColor() color.Color {
	return color.NRGBA{R: 0xA0, G: 0xA0, B: 0xA0, A: 0xFF}
}

func jsonPunctColor() color.Color {
	return color.NRGBA{R: 0xB0, G: 0xB0, B: 0xB0, A: 0xFF}
}

func jsonValueColor(v string) color.Color {
	if v == "" {
		return theme.ForegroundColor()
	}
	switch v {
	case "null":
		return jsonNullColor()
	case "true", "false":
		return jsonBoolColor()
	}
	if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
		return jsonStringColor()
	}
	if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
		return jsonPunctColor()
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return jsonNumberColor()
	}
	return theme.ForegroundColor()
}

func tokenizeQuery(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func matchNode(n *jsonTreeNode, q string, qTokens []string) bool {
	if n == nil {
		return false
	}
	if q == "" {
		return false
	}
	if strings.Contains(n.searchText, q) {
		return true
	}
	if len(qTokens) == 0 {
		return false
	}
	for _, qt := range qTokens {
		ok := false
		for _, nt := range n.tokens {
			if strings.HasPrefix(nt, qt) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func formatScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		b, _ := json.Marshal(t)
		return string(b)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", t), "0"), ".")
	default:
		b, err := json.Marshal(t)
		if err == nil {
			return string(b)
		}
		return fmt.Sprint(t)
	}
}

// escEntry provides a small helper to close the search on Escape.
type escEntry struct {
	widget.Entry
	onEsc func()
}

func newEscEntry() *escEntry {
	e := &escEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *escEntry) SetOnEsc(fn func()) {
	e.onEsc = fn
}

func (e *escEntry) TypedKey(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyEscape {
		if e.onEsc != nil {
			e.onEsc()
			return
		}
	}
	e.Entry.TypedKey(ev)
}
