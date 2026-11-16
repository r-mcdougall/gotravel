package booking

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidUUID(t *testing.T) {
	require.True(t, validUUID(testFlightID))
	require.True(t, validUUID("0B6F3F0E-6C0C-4C46-8F0B-3D6F1F0C9A11"))
	require.False(t, validUUID(""))
	require.False(t, validUUID("not-a-uuid"))
	require.False(t, validUUID("0b6f3f0e6c0c4c468f0b3d6f1f0c9a11"))
	require.False(t, validUUID("0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a1g"))
}
