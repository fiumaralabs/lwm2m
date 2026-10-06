package model

// Class is the namespace an object ID belongs to (Core App. D.2.1, Tbl
// D.2.1-1).
type Class uint8

const (
	ClassOMA             Class = iota // 0-1023, oma label
	ClassReserved                     // 1024-2047 and 42801-65534
	ClassExt                          // 2048-10240, ext label (other SDOs)
	ClassVendor                       // 10241-32768, x label
	ClassCompanyReserved              // 32769-42768, x label
	ClassTest                         // 42769-42800: MUST NOT be used in production
	ClassInvalid                      // 65535 (MAX_ID)
)

func (c Class) String() string {
	return [...]string{"oma", "reserved", "ext", "x", "x-company", "test", "invalid"}[c]
}

// ObjectClass classifies an object ID (ID-03).
func ObjectClass(id uint16) Class {
	switch {
	case id <= 1023:
		return ClassOMA
	case id <= 2047:
		return ClassReserved
	case id <= 10240:
		return ClassExt
	case id <= 32768:
		return ClassVendor
	case id <= 42768:
		return ClassCompanyReserved
	case id <= 42800:
		return ClassTest
	case id == 65535:
		return ClassInvalid
	}
	return ClassReserved
}

// URNLabel is the URN label of an object ID's class: "oma", "ext" or "x"
// (Core §7.2.2), "" for reserved and invalid IDs.
func URNLabel(id uint16) string {
	switch ObjectClass(id) {
	case ClassOMA:
		return "oma"
	case ClassExt:
		return "ext"
	case ClassVendor, ClassCompanyReserved, ClassTest:
		return "x"
	}
	return ""
}
