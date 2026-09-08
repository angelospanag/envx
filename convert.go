package envx

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// setValue parses raw into fv.
func setValue(fv reflect.Value, raw, sep string) error {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return setValue(fv.Elem(), raw, sep)
	}

	// A type that parses itself wins, which is what makes Secret, time.Time and
	// any user type work without a special case.
	if fv.CanAddr() {
		if u, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := u.UnmarshalText([]byte(raw)); err != nil {
				return fmt.Errorf("must be a valid %s: %w", fv.Type(), err)
			}
			return nil
		}
	}

	if fv.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("must be a duration such as 30s or 5m, got %q", raw)
		}
		fv.SetInt(int64(d))
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
		return nil

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("must be a boolean such as true or false, got %q", raw)
		}
		fv.SetBool(b)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "an integer", fv.Type())
		}
		fv.SetInt(n)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "a non-negative integer", fv.Type())
		}
		fv.SetUint(n)
		return nil

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return numErr(err, raw, "a number", fv.Type())
		}
		// NaN and Inf are never deliberate config values and fail silently:
		// every comparison against NaN is false, so a threshold stops working.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("must be a finite number, got %q", raw)
		}
		fv.SetFloat(f)
		return nil

	case reflect.Slice:
		return setSlice(fv, raw, sep)

	default:
		return fmt.Errorf("%w %s", ErrUnsupportedType, fv.Type())
	}
}

func setSlice(fv reflect.Value, raw, sep string) error {
	// []byte is a payload, not a list.
	if fv.Type().Elem().Kind() == reflect.Uint8 && fv.Type().Elem().PkgPath() == "" {
		fv.SetBytes([]byte(raw))
		return nil
	}
	if raw == "" {
		fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		return nil
	}
	parts := strings.Split(raw, sep)
	out := reflect.MakeSlice(fv.Type(), 0, len(parts))
	for i, p := range parts {
		// A trailing or doubled separator is the commonest way to mistype a
		// list; an empty item would become a host or token failing much later.
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		elem := reflect.New(fv.Type().Elem()).Elem()
		if err := setValue(elem, p, sep); err != nil {
			return fmt.Errorf("item %d: %w", i+1, err)
		}
		out = reflect.Append(out, elem)
	}
	fv.Set(out)
	return nil
}

func numErr(err error, raw, want string, t reflect.Type) error {
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return fmt.Errorf("must fit in %s, got %q", t, raw)
	}
	return fmt.Errorf("must be %s, got %q", want, raw)
}
