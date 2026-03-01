//go:build darwin || linux || windows

package jsontree

import (
	"encoding/json"
	"fmt"
	"sort"
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

	tree   *widget.Tree
	scroll *container.Scroll

	searchEntry *escEntry
	searchWrap  *fyne.Container

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
}

// NewSearchableJSONTree creates a JSON tree widget with an embedded search bar.
func NewSearchableJSONTree() *SearchableJSONTree {
	jt := &SearchableJSONTree{rootID: rootID}

	jt.tree = widget.NewTree(
		jt.childUIDs,
		jt.isBranch,
		func(branch bool) fyne.CanvasObject {
			lbl := canvas.NewText("", theme.ForegroundColor())
			lbl.TextSize = theme.TextSize() - 1
			lbl.TextStyle = fyne.TextStyle{Monospace: true}
			return lbl
		},
		jt.updateNode,
	)
	jt.tree.Root = jt.rootID
	jt.tree.HideSeparators = true
	jt.scroll = container.NewScroll(jt.tree)

	jt.searchEntry = newEscEntry()
	jt.searchEntry.SetPlaceHolder("Search output")
	jt.searchEntry.OnChanged = jt.onSearchChanged
	jt.searchEntry.SetOnEsc(func() {
		if jt.SearchVisible() {
			jt.SetSearchVisible(false)
		}
	})

	jt.searchWrap = container.NewGridWrap(
		fyne.NewSize(420, jt.searchEntry.MinSize().Height),
		jt.searchEntry,
	)
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
	jt.searchWrap.Objects = []fyne.CanvasObject{jt.searchEntry}
	jt.searchWrap.Resize(fyne.NewSize(w, jt.searchEntry.MinSize().Height))
	jt.searchWrap.Refresh()
}

// SetSearchVisible shows or hides the search bar and clears query when hidden.
func (jt *SearchableJSONTree) SetSearchVisible(show bool) {
	if show {
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
	lbl := obj.(*canvas.Text)
	if uid == jt.rootID {
		lbl.Text = ""
		lbl.Color = theme.ForegroundColor()
		lbl.Refresh()
		return
	}
	if jt.treeNodes == nil {
		lbl.Text = ""
		lbl.Color = theme.ForegroundColor()
		lbl.Refresh()
		return
	}
	n, ok := jt.treeNodes[uid]
	if !ok {
		lbl.Text = ""
		lbl.Color = theme.ForegroundColor()
		lbl.Refresh()
		return
	}
	value := n.value
	if len(n.children) > 0 && jt.tree.IsBranchOpen(uid) && value == "{...}" {
		value = ""
	}
	if value == "" {
		lbl.Text = n.key
	} else {
		lbl.Text = n.key + ": " + value
	}
	if jt.treeMatches != nil && jt.treeMatches[uid] {
		lbl.Color = theme.PrimaryColor()
	} else {
		lbl.Color = theme.ForegroundColor()
	}
	lbl.Refresh()
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
		jt.tree.Refresh()
		return
	}
	vis := make(map[string]bool)
	match := make(map[string]bool)

	var markDescendants func(string)
	markDescendants = func(id string) {
		n := jt.treeNodes[id]
		if n == nil {
			return
		}
		for _, c := range n.children {
			if vis[c] {
				continue
			}
			vis[c] = true
			markDescendants(c)
		}
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

	for _, id := range candidates {
		n := jt.treeNodes[id]
		if n == nil {
			continue
		}
		if !matchNode(n, q, qTokens) {
			continue
		}
		match[id] = true
		cur := id
		for cur != "" {
			vis[cur] = true
			pn := jt.treeNodes[cur]
			if pn == nil {
				break
			}
			cur = pn.parent
		}
		markDescendants(id)
	}
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
