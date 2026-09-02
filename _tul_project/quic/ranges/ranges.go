package ranges

import (
	"os"
	"sync"
)

type Range struct {
	Start int64
	End   int64
}

type ReceivedRanges struct {
	mu            sync.Mutex
	connID        string
	ranges        []Range
	currentOffset int64
}

func NewReceivedRanges(connID string) *ReceivedRanges {
	return &ReceivedRanges{
		connID: connID,
	}
}

type ReceivedBuffer struct {
	mu   sync.Mutex
	data []byte
	size int64
}

func NewReceivedBuffer(size int64) *ReceivedBuffer {
	return &ReceivedBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (rb *ReceivedBuffer) Write(offset int64, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	end := offset + int64(len(data))
	if end > rb.size {
		end = rb.size
	}
	copy(rb.data[offset:end], data[:end-offset])
}

func (rb *ReceivedBuffer) Save(path string) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return os.WriteFile(path, rb.data, 0644)
}

func CountCombinedGaps(r1, r2 *ReceivedRanges, fileSize int64) int {
	_, gaps := GetCombinedGaps(r1, r2, fileSize)
	return len(gaps)
}

func GetCombinedGaps(r1, r2 *ReceivedRanges, fileSize int64) ([]Range, []Range) {
	r1.mu.Lock()
	r2.mu.Lock()
	defer r1.mu.Unlock()
	defer r2.mu.Unlock()

	n1, n2 := len(r1.ranges), len(r2.ranges)
	if n1+n2 == 0 {
		return nil, nil
	}

	combined := make([]Range, 0, n1+n2)
	i, j := 0, 0
	for i < n1 || j < n2 {
		var cur Range
		if j >= n2 || (i < n1 && r1.ranges[i].Start < r2.ranges[j].Start) {
			cur = r1.ranges[i]
			i++
		} else {
			cur = r2.ranges[j]
			j++
		}
		if len(combined) > 0 && cur.Start <= combined[len(combined)-1].End {
			if cur.End > combined[len(combined)-1].End {
				combined[len(combined)-1].End = cur.End
			}
		} else {
			combined = append(combined, cur)
		}
	}

	var gaps []Range
	if combined[0].Start > 0 {
		gaps = append(gaps, Range{Start: 0, End: combined[0].Start})
	}
	for i := 1; i < len(combined); i++ {
		if combined[i].Start > combined[i-1].End {
			gaps = append(gaps, Range{Start: combined[i-1].End, End: combined[i].Start})
		}
	}
	last := combined[len(combined)-1]
	if last.End < fileSize {
		gaps = append(gaps, Range{Start: last.End, End: fileSize})
	}
	return combined, gaps
}

func (rr *ReceivedRanges) AddRange(start, end int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if end > rr.currentOffset {
		rr.currentOffset = end
	}

	n := len(rr.ranges)

	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if rr.ranges[mid].Start < start {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	insIdx := lo

	newStart, newEnd := start, end

	mergeStart := insIdx
	if mergeStart > 0 && rr.ranges[mergeStart-1].End >= start {
		mergeStart--
		newStart = rr.ranges[mergeStart].Start
		if rr.ranges[mergeStart].End > newEnd {
			newEnd = rr.ranges[mergeStart].End
		}
	}

	mergeEnd := insIdx
	for mergeEnd < n && rr.ranges[mergeEnd].Start <= newEnd {
		if rr.ranges[mergeEnd].End > newEnd {
			newEnd = rr.ranges[mergeEnd].End
		}
		mergeEnd++
	}

	merged := Range{Start: newStart, End: newEnd}

	if mergeEnd == mergeStart {
		rr.ranges = append(rr.ranges, Range{})
		copy(rr.ranges[insIdx+1:], rr.ranges[insIdx:])
		rr.ranges[insIdx] = merged
	} else {
		rr.ranges[mergeStart] = merged
		rr.ranges = append(rr.ranges[:mergeStart+1], rr.ranges[mergeEnd:]...)
	}
}

func (rr *ReceivedRanges) GetRanges() []Range {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	result := make([]Range, len(rr.ranges))
	copy(result, rr.ranges)
	return result
}

func (rr *ReceivedRanges) GetCurrentOffset() int64 {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.currentOffset
}

// CountGaps zwraca liczbę przerw w zakresie [0, currentOffset)
func (rr *ReceivedRanges) CountGaps() int {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	gaps := 0
	for i, r := range rr.ranges {
		if i == 0 {
			if r.Start > 0 {
				gaps++
			}
		} else if r.Start > rr.ranges[i-1].End {
			gaps++
		}
	}
	return gaps
}

// IsRangeCovered sprawdza czy dany zakres jest już pokryty
func (rr *ReceivedRanges) IsRangeCovered(start, end int64) bool {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	for _, r := range rr.ranges {
		if r.Start <= start && r.End >= end {
			return true
		}
	}
	return false
}

// GetUncoveredPortion zwraca część zakresu [start, end) która nie jest jeszcze pokryta
// Zwraca (newStart, newEnd, isCovered)
// Jeśli cały zakres jest pokryty, isCovered = true
// Jeśli część jest pokryta, zwraca niezakrytą część
func (rr *ReceivedRanges) GetUncoveredPortion(start, end int64) (int64, int64, bool) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Sprawdź czy cały zakres jest pokryty
	for _, r := range rr.ranges {
		if r.Start <= start && r.End >= end {
			return 0, 0, true
		}
	}

	// Jeśli żaden zakres nie pokrywa - zwróć cały zakres
	for _, r := range rr.ranges {
		if r.Start <= start && r.End > start {
			// Część jest pokryta od r.End
			if r.End >= end {
				return 0, 0, true
			}
			return r.End, end, false
		}
	}

	// Nic nie pokrywa, zwróć cały zakres
	return start, end, false
}
