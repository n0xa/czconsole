package tool

// CAP_NET_RAW bit position (linux/capability.h).
const capNetRaw = 13

// AllowedCapMask is the capability bitmask a tool of this class may hold. run-tool
// refuses to run if its effective caps exceed this — defense-in-depth in case it
// was started under a higher-privilege unit than the spec's class warrants.
func AllowedCapMask(class string) uint64 {
	switch class {
	case ClassNetRaw:
		return 1 << capNetRaw
	default: // ClassPlain and anything unrecognised → no caps
		return 0
	}
}
