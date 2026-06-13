# Отдельный этап: вкладка Paste (base64/hex) + Unknown fields

Эти две фичи вынесены в **отдельный этап разработки** (отдельные коммиты).
Код был откатан из основной ветки; ниже — готовый план, чтобы вернуться.

---

## Коммит 1 — Вкладка «Paste» (ввод base64/hex)

**Зачем:** payload часто приходит строкой (base64/hex), а File-вкладка читает
сырые байты файла и hex-текст не декодирует. Нужна вкладка для вставки.

**Контракт вкладки** (`internal/ui/tab/tab.go`): `Title() / View() / Fetch(ctx) ([]byte, error)`.
Новая вкладка встаёт третьей (index 2), в `layout.go` — `case 2: src = pasteTab`.

**UI:**
- Многострочное поле ввода + кнопка Clear.
- **Encoding:** Auto / Base64 / Hex (Auto: чистка пробелов, префиксы `\x`/`\\x`/`0x`;
  похоже на hex → hex, иначе base64 в вариантах Std/RawStd/URL/RawURL).
- **Mode:** Single / Array / Array (length-delimited).
- **Load file** — для больших данных грузим в буфер мимо `widget.Entry`
  (Fyne Entry виснет на сотнях КБ; см. ниже). Автовынос ввода > ~20 000 символов
  из поля в буфер. `Fetch` берёт буфер, если непустой.

**Формат массива (важно):** в БД массив лежит как Postgres `bytea[]`, где каждый
элемент — независимо сериализованный proto-message (см.
`metagame/types/club/clubpl/card_players.go`: `Players.Value()/Scan()` →
`[][]byte` из `comclub.ClubPlayer`). Это НЕ length-delimited и НЕ repeated.
Поэтому режим **Array** парсит независимые элементы:
- текст Postgres-массива `{"\x..","\x.."}` ИЛИ по одному base64/hex на строку;
- каждый элемент декодится отдельно → JSON-массив.

**Реализация декода массива:** вкладка переупаковывает элементы в length-delimited
поток (`protowire.AppendVarint(len)+payload`), а в декодере единый путь:
- `domain.DecodeRequest.Array bool`;
- `protodec`: `splitDelimited([]byte) [][]byte` + `decodeJSONArray(...)` (рефакторинг
  `decodeJSON` на `messageDescriptor` + `decodeOneCompact` + `prettyJSON`).
- `layout.go`: `arrayDecode` из `pasteTab.ArrayDecode()`, прокинуть в `DecodeRequest.Array`.
- Режим «Array (length-delimited)» — блоб как есть (Java writeDelimitedTo / Go protodelim).

**Готча Fyne Entry:** большой текст (сотни КБ, напр. `assets/meta.bin` ≈ 409 КБ hex)
вешает UI-поток. Решение — Load file (мимо Entry) + автовынос больших вставок в буфер.

**Тесты:** `splitDelimited` (+truncated); `decodeBlob` (hex/`\x`/`0x`/base64/auto);
`parseElements` (Postgres-текст / построчно / кавычки с запятыми).

---

## Коммит 2 — Unknown fields (детект неверного типа/версии)

**Зачем:** поля, которые есть в байтах, но которых нет в `.proto`, `proto.Unmarshal`
складывает в «unknown set», а мы их молча теряем. Это главный сигнал
«не тот message type / не та версия схемы».

**Слой данных:**
- `domain.DecodeResult` += `UnknownFields []UnknownField` (+ счётчик).
  `UnknownField { Path string; Field int32; Wire string; Preview string }`.
- В `protodec` после `proto.Unmarshal` рекурсивно обойти дерево `dynamicpb`-сообщения:
  `msg.ProtoReflect().GetUnknown()` на каждом уровне → парсить `protowire`'ом
  `(номер, wire type, значение)`; заходить в заполненные message-поля, элементы
  `repeated`, значения `map`. Путь: `(root)`, `items[3]`, `items[3].meta`.
  Для режима массива — пути с индексом `[i]`.
- Превью (MVP): varint → десятичное; fixed32/64 → число/hex; length-delimited →
  печатная UTF-8 строка иначе `bytes(N) 0x…` (обрезка). БЕЗ рекурсивного decode_raw
  на первом шаге. Список капить (~200) с «+N more».

**UI (выбрано: баннер над выводом):**
- Тонкая янтарная полоса между панелью поиска и выводом, скрыта при unknown=0.
- Свёрнуто: `⚠ N unknown fields — возможно неверный тип/версия   [Show ▾] [×]`.
- Развёрнуто: моноширинная таблица `path / field / wire / value` в скролле
  (≈6–8 строк, дальше прокрутка).
- Обновляется после каждого успешного декода; сбрасывается при новом декоде/Clear.
- В `layout.go` — виджет над `outputStack`, заполняется из `res.UnknownFields`.

**Возможное развитие:** рекурсивный decode_raw для вложенных unknown-сообщений;
клик по строке — копирование; подсказка «какой тип мог бы подойти» по набору
неизвестных номеров полей.