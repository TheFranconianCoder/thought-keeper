// Package idpath derives the canonical, sharded file paths of ULID-addressed objects.
//
// A ULID starts with a timestamp; the entropy is at its END. Sharding on the head would put every
// object created in one run into the same folder, so both atoms and cluster records use the tail.
package idpath

import (
	"fmt"

	"github.com/oklog/ulid/v2"
)

// Shard is the two-level folder of an object: the two bytes before the final byte pair.
func Shard(id ulid.ULID) string {
	return fmt.Sprintf("%02x/%02x", id[15], id[14])
}
