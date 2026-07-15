package clients

import (
	"encoding/binary"
	"math/bits"
	"net/netip"
	"sort"
)

type ipv4Range struct {
	start uint32
	end   uint32
}

func prefixesToIPv4Ranges(prefixes []string) []ipv4Range {
	if len(prefixes) == 0 {
		return nil
	}

	ranges := make([]ipv4Range, 0, len(prefixes))

	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}

		prefix = prefix.Masked()
		addr := prefix.Addr().As4()
		start := binary.BigEndian.Uint32(addr[:])
		blockSize := uint64(1) << uint(32-prefix.Bits())

		ranges = append(ranges, ipv4Range{
			start: start,
			end:   uint32(uint64(start) + blockSize - 1),
		})
	}

	return ranges
}

func mergeIPv4Ranges(ranges []ipv4Range) []ipv4Range {
	if len(ranges) == 0 {
		return nil
	}

	sorted := append([]ipv4Range(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].start == sorted[j].start {
			return sorted[i].end < sorted[j].end
		}

		return sorted[i].start < sorted[j].start
	})

	merged := make([]ipv4Range, 0, len(sorted))
	merged = append(merged, sorted[0])

	for _, current := range sorted[1:] {
		last := &merged[len(merged)-1]
		if uint64(current.start) <= uint64(last.end)+1 {
			if current.end > last.end {
				last.end = current.end
			}
			continue
		}

		merged = append(merged, current)
	}

	return merged
}

func subtractIPv4Ranges(base, excluded []ipv4Range) []ipv4Range {
	base = mergeIPv4Ranges(base)
	excluded = mergeIPv4Ranges(excluded)
	if len(base) == 0 || len(excluded) == 0 {
		return base
	}

	remaining := make([]ipv4Range, 0, len(base)+len(excluded))
	exclusionIndex := 0

	for _, baseRange := range base {
		cursor := uint64(baseRange.start)
		baseEnd := uint64(baseRange.end)

		for exclusionIndex < len(excluded) && uint64(excluded[exclusionIndex].end) < cursor {
			exclusionIndex++
		}

		currentExclusion := exclusionIndex
		for currentExclusion < len(excluded) && uint64(excluded[currentExclusion].start) <= baseEnd {
			exclusionStart := uint64(excluded[currentExclusion].start)
			exclusionEnd := uint64(excluded[currentExclusion].end)

			if exclusionStart > cursor {
				remaining = append(remaining, ipv4Range{
					start: uint32(cursor),
					end:   uint32(exclusionStart - 1),
				})
			}

			cursor = exclusionEnd + 1
			if cursor > baseEnd {
				break
			}

			currentExclusion++
		}

		exclusionIndex = currentExclusion
		if cursor <= baseEnd {
			remaining = append(remaining, ipv4Range{
				start: uint32(cursor),
				end:   baseRange.end,
			})
		}
	}

	return remaining
}

func ipv4RangesToPrefixes(ranges []ipv4Range) []string {
	ranges = mergeIPv4Ranges(ranges)
	if len(ranges) == 0 {
		return nil
	}

	prefixes := make([]string, 0, len(ranges))

	for _, current := range ranges {
		cursor := uint64(current.start)
		end := uint64(current.end)

		for cursor <= end {
			alignmentBits := bits.TrailingZeros64(cursor)
			if alignmentBits > 32 {
				alignmentBits = 32
			}

			blockSize := uint64(1) << uint(alignmentBits)
			remaining := end - cursor + 1
			for blockSize > remaining {
				blockSize >>= 1
			}

			var addrBytes [4]byte
			binary.BigEndian.PutUint32(addrBytes[:], uint32(cursor))

			prefixBits := 32 - bits.TrailingZeros64(blockSize)
			prefixes = append(prefixes, netip.PrefixFrom(netip.AddrFrom4(addrBytes), prefixBits).String())
			cursor += blockSize
		}
	}

	return prefixes
}
