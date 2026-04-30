package protocol

import (
	"testing"
	"unsafe"
)

func TestWireStructSizes(t *testing.T) {
	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{name: "Entry", got: unsafe.Sizeof(Entry{}), want: EntrySize},
		{name: "ReadRequest", got: unsafe.Sizeof(ReadRequest{}), want: ReadRequestSize},
		{name: "ReadResponseHeader", got: unsafe.Sizeof(ReadResponseHeader{}), want: ReadHeaderSize},
		{name: "SearchRequest", got: unsafe.Sizeof(SearchRequest{}), want: SearchRequestSize},
		{name: "SearchResult", got: unsafe.Sizeof(SearchResult{}), want: SearchResultSize},
		{name: "SearchResponseHeader", got: unsafe.Sizeof(SearchResponseHeader{}), want: SearchHeaderSize},
		{name: "PTERequest", got: unsafe.Sizeof(PTERequest{}), want: PTERequestSize},
		{name: "PTEPage", got: unsafe.Sizeof(PTEPage{}), want: PTEPageSize},
		{name: "PTEResponseHeader", got: unsafe.Sizeof(PTEResponseHeader{}), want: PTEHeaderSize},
		{name: "HashPolicyRequest", got: unsafe.Sizeof(HashPolicyRequest{}), want: HashPolicySize},
		{name: "TestAllocRequest", got: unsafe.Sizeof(TestAllocRequest{}), want: TestAllocRequestSize},
		{name: "TestAllocResponse", got: unsafe.Sizeof(TestAllocResponse{}), want: TestAllocResponseSize},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Fatalf("%s size mismatch: got %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}
