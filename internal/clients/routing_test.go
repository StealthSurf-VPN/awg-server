package clients

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeRouting(t *testing.T) {
	tooManyAllowed := make([]string, 4097)
	for i := range tooManyAllowed {
		tooManyAllowed[i] = "10.0.0.0/8"
	}

	tests := []struct {
		name      string
		routing   *Routing
		want      *Routing
		wantField string
	}{
		{name: "nil is full tunnel"},
		{name: "explicit full is canonical nil", routing: &Routing{Mode: "full"}},
		{
			name: "split masks and deduplicates stably",
			routing: &Routing{
				Mode:       "split",
				AllowedIPs: []string{"10.1.2.3/8", "172.16.1.1/12", "10.0.0.0/8"},
			},
			want: &Routing{
				Mode:       "split",
				AllowedIPs: []string{"10.0.0.0/8", "172.16.0.0/12"},
			},
		},
		{
			name: "bypass masks exclusions",
			routing: &Routing{
				Mode:        "bypass",
				ExcludedIPs: []string{"192.168.1.7/16", "10.1.2.3/8"},
			},
			want: &Routing{
				Mode:        "bypass",
				ExcludedIPs: []string{"192.168.0.0/16", "10.0.0.0/8"},
			},
		},
		{name: "full rejects allowed", routing: &Routing{Mode: "full", AllowedIPs: []string{"10.0.0.0/8"}}, wantField: "routing.allowed_ips"},
		{name: "bypass requires exclusions", routing: &Routing{Mode: "bypass"}, wantField: "routing.excluded_ips"},
		{name: "split requires allowed", routing: &Routing{Mode: "split"}, wantField: "routing.allowed_ips"},
		{name: "rejects ipv6", routing: &Routing{Mode: "split", AllowedIPs: []string{"::/0"}}, wantField: "routing.allowed_ips[0]"},
		{
			name: "rejects complete exclusion",
			routing: &Routing{
				Mode:        "split",
				AllowedIPs:  []string{"10.0.0.0/8"},
				ExcludedIPs: []string{"10.0.0.0/8"},
			},
			wantField: "routing.excluded_ips",
		},
		{
			name:      "limits input before deduplication",
			routing:   &Routing{Mode: "split", AllowedIPs: tooManyAllowed},
			wantField: "routing.allowed_ips",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeRouting(tt.routing)
			assertRoutingField(t, err, tt.wantField)
			if tt.wantField == "" && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeRouting() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRoutingAllowedIPs(t *testing.T) {
	tests := []struct {
		name    string
		routing *Routing
		want    string
		wantErr bool
	}{
		{name: "nil full tunnel", want: "0.0.0.0/0, ::/0"},
		{name: "explicit full tunnel", routing: &Routing{Mode: "full"}, want: "0.0.0.0/0, ::/0"},
		{
			name: "split without exclusions",
			routing: &Routing{
				Mode:       "split",
				AllowedIPs: []string{"10.0.0.0/8", "172.16.0.0/12"},
			},
			want: "10.0.0.0/8, 172.16.0.0/12",
		},
		{
			name: "split subtracts exclusions",
			routing: &Routing{
				Mode:        "split",
				AllowedIPs:  []string{"10.0.0.0/29"},
				ExcludedIPs: []string{"10.0.0.2/31"},
			},
			want: "10.0.0.0/31, 10.0.0.4/30",
		},
		{
			name: "bypass retains ipv6",
			routing: &Routing{
				Mode:        "bypass",
				ExcludedIPs: []string{"0.0.0.0/1"},
			},
			want: "128.0.0.0/1, ::/0",
		},
		{name: "invalid mode", routing: &Routing{Mode: "invalid"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := routingAllowedIPs(tt.routing)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRouting) {
					t.Fatalf("routingAllowedIPs() error = %v, want ErrInvalidRouting", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("routingAllowedIPs() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("routingAllowedIPs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIPv4RangeOperations(t *testing.T) {
	t.Run("merge sorts overlaps and adjacency", func(t *testing.T) {
		input := []ipv4Range{
			{start: 5, end: 10},
			{start: 0, end: 2},
			{start: 3, end: 4},
			{start: 8, end: 12},
			{start: 0xffffffff, end: 0xffffffff},
		}
		want := []ipv4Range{
			{start: 0, end: 12},
			{start: 0xffffffff, end: 0xffffffff},
		}
		if got := mergeIPv4Ranges(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("mergeIPv4Ranges() = %+v, want %+v", got, want)
		}
	})

	t.Run("subtract handles overlaps and base gaps", func(t *testing.T) {
		base := []ipv4Range{{start: 0, end: 15}, {start: 20, end: 25}}
		excluded := []ipv4Range{{start: 4, end: 7}, {start: 10, end: 22}}
		want := []ipv4Range{{start: 0, end: 3}, {start: 8, end: 9}, {start: 23, end: 25}}
		if got := subtractIPv4Ranges(base, excluded); !reflect.DeepEqual(got, want) {
			t.Fatalf("subtractIPv4Ranges() = %+v, want %+v", got, want)
		}
	})

	tests := []struct {
		name  string
		input []ipv4Range
		want  []string
	}{
		{name: "complete address space", input: []ipv4Range{{start: 0, end: 0xffffffff}}, want: []string{"0.0.0.0/0"}},
		{name: "last address", input: []ipv4Range{{start: 0xffffffff, end: 0xffffffff}}, want: []string{"255.255.255.255/32"}},
		{
			name:  "unaligned range",
			input: []ipv4Range{{start: 0x0a000001, end: 0x0a000006}},
			want:  []string{"10.0.0.1/32", "10.0.0.2/31", "10.0.0.4/31", "10.0.0.6/32"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipv4RangesToPrefixes(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ipv4RangesToPrefixes() = %v, want %v", got, tt.want)
			}
			if roundTrip := mergeIPv4Ranges(prefixesToIPv4Ranges(got)); !reflect.DeepEqual(roundTrip, mergeIPv4Ranges(tt.input)) {
				t.Fatalf("prefix round trip = %+v, want %+v", roundTrip, tt.input)
			}
		})
	}
}

func TestEffectiveRoutingReturnsDefensiveCopy(t *testing.T) {
	if got := EffectiveRouting(nil); got.Mode != "full" {
		t.Fatalf("EffectiveRouting(nil) mode = %q, want %q", got.Mode, "full")
	}

	original := &Routing{
		Mode:        "split",
		AllowedIPs:  []string{"10.0.0.0/8"},
		ExcludedIPs: []string{"10.1.0.0/16"},
	}
	got := EffectiveRouting(original)
	got.AllowedIPs[0] = "192.168.0.0/16"
	got.ExcludedIPs[0] = "192.168.1.0/24"

	if original.AllowedIPs[0] != "10.0.0.0/8" || original.ExcludedIPs[0] != "10.1.0.0/16" {
		t.Fatalf("EffectiveRouting() returned aliased slices: %+v", original)
	}
}

func assertRoutingField(t *testing.T, err error, wantField string) {
	t.Helper()

	if wantField == "" {
		if err != nil {
			t.Fatalf("unexpected routing error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected routing error for %s", wantField)
	}
	if !errors.Is(err, ErrInvalidRouting) {
		t.Fatalf("error %v does not wrap ErrInvalidRouting", err)
	}

	var routingErr *RoutingValidationError
	if !errors.As(err, &routingErr) {
		t.Fatalf("error %v is not a RoutingValidationError", err)
	}
	if routingErr.Field != wantField {
		t.Fatalf("routing field = %q, want %q", routingErr.Field, wantField)
	}
}
