package sort

// less is compares two []byte slices lexicographically, without allocations.
func less(a, b []byte) bool {
	m := len(a)
	if len(b) < m {
		m = len(b)
	}
	for i := 0; i < m; i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return len(a) < len(b)
}

// KVSlice sorts [][2][]byte in-place by kv[i][0], using insertion sort.
func KVSlice(kv [][2][]byte) {
	for i := 1; i < len(kv); i++ {
		for j := i; j > 0 && less(kv[j][0], kv[j-1][0]); j-- {
			kv[j], kv[j-1] = kv[j-1], kv[j]
		}
	}
}
