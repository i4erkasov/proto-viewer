package protodec

import (
	"context"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/i4erkasov/proto-viewer/internal/domain"
)

// guessMaxResults — сколько кандидатов отдаём в UI (ранжированных).
const guessMaxResults = 15

// GuessType перебирает все message-типы из набора .proto и пытается разобрать
// payload каждым из них. Тип «подходит», если proto.Unmarshal успешен (wire-типы
// присутствующих полей совместимы); ранжируем по числу заполненных полей и
// отсутствию нераспознанных (unknown) данных. Возвращает топ кандидатов.
//
// Это эвристика: protobuf на уровне байт не несёт имени типа, поэтому возможна
// неоднозначность — отдаём список, выбор за пользователем.
func (d *Decoder) GuessType(ctx context.Context, protoRoot, protoAbs string, payload []byte) ([]domain.TypeGuess, error) {
	inner, _ := detectEncoding(payload) // снимаем обёртки (gzip/base64/hex/zlib)
	if len(inner) == 0 {
		return nil, nil
	}

	// Если конкретный Proto file не задан — ищем по всем .proto под root.
	var fds *descriptorpb.FileDescriptorSet
	var err error
	if strings.TrimSpace(protoAbs) == "" {
		fds, err = compileAllUnderRoot(ctx, protoRoot)
	} else {
		fds, err = compileDescriptorSet(ctx, protoRoot, protoAbs)
	}
	if err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(fds)
	if err != nil {
		return nil, err
	}

	// Собираем все message-дескрипторы (рекурсивно), кроме map-entry и WKT.
	var mds []protoreflect.MessageDescriptor
	var collect func(protoreflect.MessageDescriptors)
	collect = func(list protoreflect.MessageDescriptors) {
		for i := 0; i < list.Len(); i++ {
			md := list.Get(i)
			if !md.IsMapEntry() {
				mds = append(mds, md)
			}
			collect(md.Messages())
		}
	}
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		collect(fd.Messages())
		return true
	})

	type scored struct {
		g     domain.TypeGuess
		score int
	}
	var out []scored
	for _, md := range mds {
		name := string(md.FullName())
		if strings.HasPrefix(name, "google.protobuf.") {
			continue
		}
		msg := dynamicpb.NewMessage(md)
		// AllowPartial: не падаем из-за отсутствующих required (proto2).
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(inner, msg); err != nil {
			continue // несовместимый wire-format → тип не подходит
		}
		fields, unknown, depth := analyzeMessage(msg, 0)
		if fields == 0 {
			continue // ничего не легло в поля — мимо
		}

		score := fields * 100
		if unknown == 0 {
			score += 1_000_000 // схема полностью объясняет данные
		}
		score += depth * 5
		score -= unknown

		out = append(out, scored{
			g: domain.TypeGuess{
				FullType:  name,
				ProtoFile: md.ParentFile().Path(), // путь относительно root (slash)
				Fields:    fields,
				Unknown:   unknown,
				Depth:     depth,
			},
			score: score,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > guessMaxResults {
		out = out[:guessMaxResults]
	}
	res := make([]domain.TypeGuess, len(out))
	for i := range out {
		res[i] = out[i].g
	}
	return res, nil
}

// analyzeMessage рекурсивно считает: число заполненных полей, суммарный объём
// нераспознанных (unknown) байт и максимальную глубину вложенности.
func analyzeMessage(m protoreflect.Message, depth int) (fields, unknown, maxDepth int) {
	unknown = len(m.GetUnknown())
	maxDepth = depth
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		fields++
		recurse := func(sub protoreflect.Message) {
			f, u, dd := analyzeMessage(sub, depth+1)
			fields += f
			unknown += u
			if dd > maxDepth {
				maxDepth = dd
			}
		}
		switch {
		case fd.IsMap():
			if isMessageKind(fd.MapValue().Kind()) {
				v.Map().Range(func(_ protoreflect.MapKey, vv protoreflect.Value) bool {
					recurse(vv.Message())
					return true
				})
			}
		case fd.IsList():
			if isMessageKind(fd.Kind()) {
				lst := v.List()
				for i := 0; i < lst.Len(); i++ {
					recurse(lst.Get(i).Message())
				}
			}
		default:
			if isMessageKind(fd.Kind()) {
				recurse(v.Message())
			}
		}
		return true
	})
	return
}

func isMessageKind(k protoreflect.Kind) bool {
	return k == protoreflect.MessageKind || k == protoreflect.GroupKind
}
