package toml

import (
	"bytes"
	"encoding"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Marshal serializes a Go value as a TOML document.
//
// It is a shortcut for Encoder.Encode() with the default options.
func Marshal(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	err := enc.Encode(v)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// Encoder writes a TOML document to an output stream.
type Encoder struct {
	// output
	w io.Writer

	// global settings
	tablesInline    bool
	arraysMultiline bool
	indentSymbol    string
	indentTables    bool
}

// NewEncoder returns a new Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{
		w:            w,
		indentSymbol: "  ",
	}
}

// SetTablesInline forces the encoder to emit all tables inline.
//
// This behavior can be controlled on an individual struct field basis with the
// inline tag:
//
//   MyField `inline:"true"`
func (enc *Encoder) SetTablesInline(inline bool) *Encoder {
	enc.tablesInline = inline
	return enc
}

// SetArraysMultiline forces the encoder to emit all arrays with one element per
// line.
//
// This behavior can be controlled on an individual struct field basis with the multiline tag:
//
//   MyField `multiline:"true"`
func (enc *Encoder) SetArraysMultiline(multiline bool) *Encoder {
	enc.arraysMultiline = multiline
	return enc
}

// SetIndentSymbol defines the string that should be used for indentation. The
// provided string is repeated for each indentation level. Defaults to two
// spaces.
func (enc *Encoder) SetIndentSymbol(s string) *Encoder {
	enc.indentSymbol = s
	return enc
}

// SetIndentTables forces the encoder to intent tables and array tables.
func (enc *Encoder) SetIndentTables(indent bool) *Encoder {
	enc.indentTables = indent
	return enc
}

// Encode writes a TOML representation of v to the stream.
//
// If v cannot be represented to TOML it returns an error.
//
// Encoding rules
//
// A top level slice containing only maps or structs is encoded as [[table
// array]].
//
// All slices not matching rule 1 are encoded as [array]. As a result, any map
// or struct they contain is encoded as an {inline table}.
//
// Nil interfaces and nil pointers are not supported.
//
// Keys in key-values always have one part.
//
// Intermediate tables are always printed.
//
// By default, strings are encoded as literal string, unless they contain either
// a newline character or a single quote. In that case they are emitted as
// quoted strings.
//
// When encoding structs, fields are encoded in order of definition, with their
// exact name.
//
// Struct tags
//
// The encoding of each public struct field can be customized by the format
// string in the "toml" key of the struct field's tag. This follows
// encoding/json's convention. The format string starts with the name of the
// field, optionally followed by a comma-separated list of options. The name may
// be empty in order to provide options without overriding the default name.
//
// The "multiline" option emits strings as quoted multi-line TOML strings. It
// has no effect on fields that would not be encoded as strings.
//
// The "inline" option turns fields that would be emitted as tables into inline
// tables instead. It has no effect on other fields.
//
// The "omitempty" option prevents empty values or groups from being emitted.
//
// In addition to the "toml" tag struct tag, a "comment" tag can be used to emit
// a TOML comment before the value being annotated. Comments are ignored inside
// inline tables.
func (enc *Encoder) Encode(v interface{}) error {
	var (
		b   []byte
		ctx encoderCtx
	)

	ctx.inline = enc.tablesInline

	if v == nil {
		return fmt.Errorf("toml: cannot encode a nil interface")
	}

	b, err := enc.encode(b, ctx, reflect.ValueOf(v))
	if err != nil {
		return err
	}

	_, err = enc.w.Write(b)
	if err != nil {
		return fmt.Errorf("toml: cannot write: %w", err)
	}

	return nil
}

type valueOptions struct {
	multiline bool
	omitempty bool
	comment   string
}

type encoderCtx struct {
	// Current top-level key.
	parentKey []string

	// Key that should be used for a KV.
	key string
	// Extra flag to account for the empty string
	hasKey bool

	// Set to true to indicate that the encoder is inside a KV, so that all
	// tables need to be inlined.
	insideKv bool

	// Set to true to skip the first table header in an array table.
	skipTableHeader bool

	// Should the next table be encoded as inline
	inline bool

	// Indentation level
	indent int

	// Options coming from struct tags
	options valueOptions
}

func (ctx *encoderCtx) shiftKey() {
	if ctx.hasKey {
		ctx.parentKey = append(ctx.parentKey, ctx.key)
		ctx.clearKey()
	}
}

func (ctx *encoderCtx) setKey(k string) {
	ctx.key = k
	ctx.hasKey = true
}

func (ctx *encoderCtx) clearKey() {
	ctx.key = ""
	ctx.hasKey = false
}

func (ctx *encoderCtx) isRoot() bool {
	return len(ctx.parentKey) == 0 && !ctx.hasKey
}

func (enc *Encoder) encode(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	if !v.IsZero() {
		i, ok := v.Interface().(time.Time)
		if ok {
			return i.AppendFormat(b, time.RFC3339), nil
		}
	}

	hasTextMarshaler := v.Type().Implements(textMarshalerType)
	if hasTextMarshaler || (v.CanAddr() && reflect.PtrTo(v.Type()).Implements(textMarshalerType)) {
		if !hasTextMarshaler {
			v = v.Addr()
		}

		if ctx.isRoot() {
			return nil, fmt.Errorf("toml: type %s implementing the TextMarshaler interface cannot be a root element", v.Type())
		}

		text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return nil, err
		}

		b = enc.encodeString(b, string(text), ctx.options)

		return b, nil
	}

	switch v.Kind() {
	// containers
	case reflect.Map:
		return enc.encodeMap(b, ctx, v)
	case reflect.Struct:
		return enc.encodeStruct(b, ctx, v)
	case reflect.Slice:
		return enc.encodeSlice(b, ctx, v)
	case reflect.Interface:
		if v.IsNil() {
			return nil, fmt.Errorf("toml: encoding a nil interface is not supported")
		}

		return enc.encode(b, ctx, v.Elem())
	case reflect.Ptr:
		if v.IsNil() {
			return enc.encode(b, ctx, reflect.Zero(v.Type().Elem()))
		}

		return enc.encode(b, ctx, v.Elem())

	// values
	case reflect.String:
		b = enc.encodeString(b, v.String(), ctx.options)
	case reflect.Float32:
		if math.Trunc(v.Float()) == v.Float() {
			b = strconv.AppendFloat(b, v.Float(), 'f', 1, 32)
		} else {
			b = strconv.AppendFloat(b, v.Float(), 'f', -1, 32)
		}
	case reflect.Float64:
		if math.Trunc(v.Float()) == v.Float() {
			b = strconv.AppendFloat(b, v.Float(), 'f', 1, 64)
		} else {
			b = strconv.AppendFloat(b, v.Float(), 'f', -1, 64)
		}
	case reflect.Bool:
		if v.Bool() {
			b = append(b, "true"...)
		} else {
			b = append(b, "false"...)
		}
	case reflect.Uint64, reflect.Uint32, reflect.Uint16, reflect.Uint8, reflect.Uint:
		b = strconv.AppendUint(b, v.Uint(), 10)
	case reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8, reflect.Int:
		b = strconv.AppendInt(b, v.Int(), 10)
	default:
		return nil, fmt.Errorf("toml: cannot encode value of type %s", v.Kind())
	}

	return b, nil
}

func isNil(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map:
		return v.IsNil()
	default:
		return false
	}
}

func (enc *Encoder) encodeKv(b []byte, ctx encoderCtx, options valueOptions, v reflect.Value) ([]byte, error) {
	var err error

	if !ctx.hasKey {
		panic("caller of encodeKv should have set the key in the context")
	}

	if (ctx.options.omitempty || options.omitempty) && isEmptyValue(v) {
		return b, nil
	}

	if !ctx.inline {
		b = enc.encodeComment(ctx.indent, options.comment, b)
	}

	b = enc.indent(ctx.indent, b)

	b, err = enc.encodeKey(b, ctx.key)
	if err != nil {
		return nil, err
	}

	b = append(b, " = "...)

	// create a copy of the context because the value of a KV shouldn't
	// modify the global context.
	subctx := ctx
	subctx.insideKv = true
	subctx.shiftKey()
	subctx.options = options

	b, err = enc.encode(b, subctx, v)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

const literalQuote = '\''

func (enc *Encoder) encodeString(b []byte, v string, options valueOptions) []byte {
	if needsQuoting(v) {
		return enc.encodeQuotedString(options.multiline, b, v)
	}

	return enc.encodeLiteralString(b, v)
}

func needsQuoting(v string) bool {
	return strings.ContainsAny(v, "'\b\f\n\r\t")
}

// caller should have checked that the string does not contain new lines or ' .
func (enc *Encoder) encodeLiteralString(b []byte, v string) []byte {
	b = append(b, literalQuote)
	b = append(b, v...)
	b = append(b, literalQuote)

	return b
}

//nolint:cyclop
func (enc *Encoder) encodeQuotedString(multiline bool, b []byte, v string) []byte {
	stringQuote := `"`

	if multiline {
		stringQuote = `"""`
	}

	b = append(b, stringQuote...)
	if multiline {
		b = append(b, '\n')
	}

	const (
		hextable = "0123456789ABCDEF"
		// U+0000 to U+0008, U+000A to U+001F, U+007F
		nul = 0x0
		bs  = 0x8
		lf  = 0xa
		us  = 0x1f
		del = 0x7f
	)

	for _, r := range []byte(v) {
		switch r {
		case '\\':
			b = append(b, `\\`...)
		case '"':
			b = append(b, `\"`...)
		case '\b':
			b = append(b, `\b`...)
		case '\f':
			b = append(b, `\f`...)
		case '\n':
			if multiline {
				b = append(b, r)
			} else {
				b = append(b, `\n`...)
			}
		case '\r':
			b = append(b, `\r`...)
		case '\t':
			b = append(b, `\t`...)
		default:
			switch {
			case r >= nul && r <= bs, r >= lf && r <= us, r == del:
				b = append(b, `\u00`...)
				b = append(b, hextable[r>>4])
				b = append(b, hextable[r&0x0f])
			default:
				b = append(b, r)
			}
		}
	}

	b = append(b, stringQuote...)

	return b
}

// called should have checked that the string is in A-Z / a-z / 0-9 / - / _ .
func (enc *Encoder) encodeUnquotedKey(b []byte, v string) []byte {
	return append(b, v...)
}

func (enc *Encoder) encodeTableHeader(ctx encoderCtx, b []byte) ([]byte, error) {
	if len(ctx.parentKey) == 0 {
		return b, nil
	}

	b = enc.encodeComment(ctx.indent, ctx.options.comment, b)

	b = enc.indent(ctx.indent, b)

	b = append(b, '[')

	var err error

	b, err = enc.encodeKey(b, ctx.parentKey[0])
	if err != nil {
		return nil, err
	}

	for _, k := range ctx.parentKey[1:] {
		b = append(b, '.')

		b, err = enc.encodeKey(b, k)
		if err != nil {
			return nil, err
		}
	}

	b = append(b, "]\n"...)

	return b, nil
}

//nolint:cyclop
func (enc *Encoder) encodeKey(b []byte, k string) ([]byte, error) {
	needsQuotation := false
	cannotUseLiteral := false

	for _, c := range k {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}

		if c == '\n' {
			return nil, fmt.Errorf("toml: new line characters in keys are not supported")
		}

		if c == literalQuote {
			cannotUseLiteral = true
		}

		needsQuotation = true
	}

	switch {
	case cannotUseLiteral:
		return enc.encodeQuotedString(false, b, k), nil
	case needsQuotation:
		return enc.encodeLiteralString(b, k), nil
	default:
		return enc.encodeUnquotedKey(b, k), nil
	}
}

func (enc *Encoder) encodeMap(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	if v.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("toml: type %s is not supported as a map key", v.Type().Key().Kind())
	}

	var (
		t                 table
		emptyValueOptions valueOptions
	)

	iter := v.MapRange()
	for iter.Next() {
		k := iter.Key().String()
		v := iter.Value()

		if isNil(v) {
			continue
		}

		if willConvertToTableOrArrayTable(ctx, v) {
			t.pushTable(k, v, emptyValueOptions)
		} else {
			t.pushKV(k, v, emptyValueOptions)
		}
	}

	sortEntriesByKey(t.kvs)
	sortEntriesByKey(t.tables)

	return enc.encodeTable(b, ctx, t)
}

func sortEntriesByKey(e []entry) {
	sort.Slice(e, func(i, j int) bool {
		return e[i].Key < e[j].Key
	})
}

type entry struct {
	Key     string
	Value   reflect.Value
	Options valueOptions
}

type table struct {
	kvs    []entry
	tables []entry
}

func (t *table) pushKV(k string, v reflect.Value, options valueOptions) {
	t.kvs = append(t.kvs, entry{Key: k, Value: v, Options: options})
}

func (t *table) pushTable(k string, v reflect.Value, options valueOptions) {
	t.tables = append(t.tables, entry{Key: k, Value: v, Options: options})
}

func (enc *Encoder) encodeStruct(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	var t table

	// TODO: cache this
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		fieldType := typ.Field(i)

		// only consider exported fields
		if fieldType.PkgPath != "" {
			continue
		}

		k := fieldType.Name

		tag := fieldType.Tag.Get("toml")

		// special field name to skip field
		if tag == "-" {
			continue
		}

		name, opts := parseTag(tag)
		if isValidName(name) {
			k = name
		}

		f := v.Field(i)

		if isNil(f) {
			continue
		}

		options := valueOptions{
			multiline: opts.multiline,
			omitempty: opts.omitempty,
			comment:   fieldType.Tag.Get("comment"),
		}

		if opts.inline || !willConvertToTableOrArrayTable(ctx, f) {
			t.pushKV(k, f, options)
		} else {
			t.pushTable(k, f, options)
		}
	}

	return enc.encodeTable(b, ctx, t)
}

func (enc *Encoder) encodeComment(indent int, comment string, b []byte) []byte {
	if comment != "" {
		b = enc.indent(indent, b)
		b = append(b, "# "...)
		b = append(b, comment...)
		b = append(b, '\n')
	}
	return b
}

func isValidName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote chars are reserved, but
			// otherwise any punctuation chars are allowed
			// in a tag name.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

type tagOptions struct {
	multiline bool
	inline    bool
	omitempty bool
}

func parseTag(tag string) (string, tagOptions) {
	opts := tagOptions{}

	idx := strings.Index(tag, ",")
	if idx == -1 {
		return tag, opts
	}

	raw := tag[idx+1:]
	tag = string(tag[:idx])
	for raw != "" {
		var o string
		i := strings.Index(raw, ",")
		if i >= 0 {
			o, raw = raw[:i], raw[i+1:]
		} else {
			o, raw = raw, ""
		}
		switch o {
		case "multiline":
			opts.multiline = true
		case "inline":
			opts.inline = true
		case "omitempty":
			opts.omitempty = true
		}
	}

	return tag, opts
}

func (enc *Encoder) encodeTable(b []byte, ctx encoderCtx, t table) ([]byte, error) {
	var err error

	ctx.shiftKey()

	if ctx.insideKv || (ctx.inline && !ctx.isRoot()) {
		return enc.encodeTableInline(b, ctx, t)
	}

	if !ctx.skipTableHeader {
		b, err = enc.encodeTableHeader(ctx, b)
		if err != nil {
			return nil, err
		}

		if enc.indentTables && len(ctx.parentKey) > 0 {
			ctx.indent++
		}
	}
	ctx.skipTableHeader = false

	for _, kv := range t.kvs {
		ctx.setKey(kv.Key)

		b, err = enc.encodeKv(b, ctx, kv.Options, kv.Value)
		if err != nil {
			return nil, err
		}

		b = append(b, '\n')
	}

	for _, table := range t.tables {
		ctx.setKey(table.Key)

		ctx.options = table.Options

		b, err = enc.encode(b, ctx, table.Value)
		if err != nil {
			return nil, err
		}

		b = append(b, '\n')
	}

	return b, nil
}

func (enc *Encoder) encodeTableInline(b []byte, ctx encoderCtx, t table) ([]byte, error) {
	var err error

	b = append(b, '{')

	first := true
	for _, kv := range t.kvs {
		if first {
			first = false
		} else {
			b = append(b, `, `...)
		}

		ctx.setKey(kv.Key)

		b, err = enc.encodeKv(b, ctx, kv.Options, kv.Value)
		if err != nil {
			return nil, err
		}
	}

	if len(t.tables) > 0 {
		panic("inline table cannot contain nested tables, online key-values")
	}

	b = append(b, "}"...)

	return b, nil
}

func willConvertToTable(ctx encoderCtx, v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	if v.Type() == timeType || v.Type().Implements(textMarshalerType) || (v.Kind() != reflect.Ptr && v.CanAddr() && reflect.PtrTo(v.Type()).Implements(textMarshalerType)) {
		return false
	}

	t := v.Type()
	switch t.Kind() {
	case reflect.Map, reflect.Struct:
		return !ctx.inline
	case reflect.Interface:
		return willConvertToTable(ctx, v.Elem())
	case reflect.Ptr:
		if v.IsNil() {
			return false
		}

		return willConvertToTable(ctx, v.Elem())
	default:
		return false
	}
}

func willConvertToTableOrArrayTable(ctx encoderCtx, v reflect.Value) bool {
	t := v.Type()

	if t.Kind() == reflect.Interface {
		return willConvertToTableOrArrayTable(ctx, v.Elem())
	}

	if t.Kind() == reflect.Slice {
		if v.Len() == 0 {
			// An empty slice should be a kv = [].
			return false
		}

		for i := 0; i < v.Len(); i++ {
			t := willConvertToTable(ctx, v.Index(i))

			if !t {
				return false
			}
		}

		return true
	}

	return willConvertToTable(ctx, v)
}

func (enc *Encoder) encodeSlice(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	if v.Len() == 0 {
		b = append(b, "[]"...)

		return b, nil
	}

	if willConvertToTableOrArrayTable(ctx, v) {
		return enc.encodeSliceAsArrayTable(b, ctx, v)
	}

	return enc.encodeSliceAsArray(b, ctx, v)
}

// caller should have checked that v is a slice that only contains values that
// encode into tables.
func (enc *Encoder) encodeSliceAsArrayTable(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	ctx.shiftKey()

	var err error
	scratch := make([]byte, 0, 64)
	scratch = append(scratch, "[["...)

	for i, k := range ctx.parentKey {
		if i > 0 {
			scratch = append(scratch, '.')
		}

		scratch, err = enc.encodeKey(scratch, k)
		if err != nil {
			return nil, err
		}
	}

	scratch = append(scratch, "]]\n"...)
	ctx.skipTableHeader = true

	for i := 0; i < v.Len(); i++ {
		b = append(b, scratch...)

		b, err = enc.encode(b, ctx, v.Index(i))
		if err != nil {
			return nil, err
		}
	}

	return b, nil
}

func (enc *Encoder) encodeSliceAsArray(b []byte, ctx encoderCtx, v reflect.Value) ([]byte, error) {
	multiline := ctx.options.multiline || enc.arraysMultiline
	separator := ", "

	b = append(b, '[')

	subCtx := ctx
	subCtx.options = valueOptions{}

	if multiline {
		separator = ",\n"

		b = append(b, '\n')

		subCtx.indent++
	}

	var err error
	first := true

	for i := 0; i < v.Len(); i++ {
		if first {
			first = false
		} else {
			b = append(b, separator...)
		}

		if multiline {
			b = enc.indent(subCtx.indent, b)
		}

		b, err = enc.encode(b, subCtx, v.Index(i))
		if err != nil {
			return nil, err
		}
	}

	if multiline {
		b = append(b, '\n')
		b = enc.indent(ctx.indent, b)
	}

	b = append(b, ']')

	return b, nil
}

func (enc *Encoder) indent(level int, b []byte) []byte {
	for i := 0; i < level; i++ {
		b = append(b, enc.indentSymbol...)
	}

	return b
}
