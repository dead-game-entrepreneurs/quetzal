package ttl25

import (
	"encoding/binary"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func syntheticProfile() (quazal.AnyData, quazal.AnyData) {
	public := make([]byte, 8)
	binary.LittleEndian.PutUint32(public, 4)
	binary.LittleEndian.PutUint32(public[4:], 1)
	private := make([]byte, 21)
	binary.LittleEndian.PutUint32(private, 17)
	binary.LittleEndian.PutUint16(private[5:], 6)
	copy(private[7:], []byte("00000\x00"))
	return quazal.AnyData{Type: "EidosAccountPublic", Payload: public}, quazal.AnyData{Type: "EidosAccountPrivate", Payload: private}
}

func TestEidosAccountDataRecord(t *testing.T) {
	public, private := syntheticProfile()
	if err := ValidateAccountData(public, private); err != nil {
		t.Fatal(err)
	}
	for i := range private.Payload {
		short := private
		short.Payload = private.Payload[:i]
		if ValidateAccountData(public, short) == nil {
			t.Fatalf("accepted private truncation %d", i)
		}
	}
	wrong := public
	wrong.Type = private.Type
	if ValidateAccountData(wrong, private) == nil {
		t.Fatal("wrong public type")
	}
	broken := private
	broken.Payload = append([]byte(nil), private.Payload...)
	broken.Payload[5] = 255
	if ValidateAccountData(public, broken) == nil {
		t.Fatal("postal length escaped the record")
	}
	broken.Payload = append([]byte(nil), private.Payload...)
	broken.Payload[12] = 1
	if ValidateAccountData(public, broken) == nil {
		t.Fatal("missing postal terminator")
	}
	public.Payload[0] = 3
	if ValidateAccountData(public, private) == nil {
		t.Fatal("bad nested public record length")
	}
}

func TestEidosNullPostalString(t *testing.T) {
	public, _ := syntheticProfile()
	private := quazal.AnyData{Type: "EidosAccountPrivate", Payload: make([]byte, 15)}
	binary.LittleEndian.PutUint32(private.Payload, 11)
	if err := ValidateAccountData(public, private); err != nil {
		t.Fatal("SDK null String rejected", err)
	}
}
