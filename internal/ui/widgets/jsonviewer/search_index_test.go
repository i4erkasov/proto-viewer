package jsonviewer

import (
	"testing"
	"time"
)

// TestSearchFindsAllOccurrences проверяет, что триграммный индекс не теряет
// совпадения (баг: «1000» находил одно, остальные нет).
func TestSearchFindsAllOccurrences(t *testing.T) {
	v := newTestView()
	// После pretty-print каждая пара на своей строке; "1000" на 4 строках,
	// "2000" — отвлекающая (содержит "000", но не "100").
	v.SetJSON(`{"a":1000,"b":1000,"c":2000,"d":1000,"e":1000}`)

	v.applySearchAsync("1000")
	waitForSearch(v, "1000", 2*time.Second)

	v.mu.Lock()
	n := len(v.searchMatchSet)
	v.mu.Unlock()

	if n != 4 {
		t.Fatalf("expected 4 matches for \"1000\", got %d", n)
	}
}

// TestSearchCaseInsensitiveUpper — запрос ≥4 символов по тексту в ВЕРХНЕМ
// регистре (был баг: «якорь» искал lowercase-байт в сыром тексте → промах).
func TestSearchCaseInsensitiveUpper(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"name":"SALAH","note":"salah too"}`)

	v.applySearchAsync("salah")
	waitForSearch(v, "salah", 2*time.Second)

	v.mu.Lock()
	n := len(v.searchMatchSet)
	v.mu.Unlock()
	// Обе строки содержат "salah" без учёта регистра.
	if n != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d", n)
	}
}

// TestSearchShortQuery проверяет короткий запрос (< 3 символов, без триграмм).
func TestSearchShortQuery(t *testing.T) {
	v := newTestView()
	v.SetJSON(`{"aa":1,"bb":1,"aa2":1}`)
	v.applySearchAsync("aa")
	waitForSearch(v, "aa", 2*time.Second)
	v.mu.Lock()
	n := len(v.searchMatchSet)
	v.mu.Unlock()
	// "aa" и "aa2" → 2 строки.
	if n != 2 {
		t.Fatalf("expected 2 matches for \"aa\", got %d", n)
	}
}
