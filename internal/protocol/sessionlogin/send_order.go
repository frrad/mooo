package sessionlogin

// SendOrderEffectKind identifies one source-backed planned send-path effect.
type SendOrderEffectKind string

const (
	EffectAllocatePacket               SendOrderEffectKind = "allocate_packet"
	EffectDerivePacketTag              SendOrderEffectKind = "derive_packet_tag"
	EffectRegisterCompletionByUID      SendOrderEffectKind = "register_completion_by_uid"
	EffectRegisterPacketUID            SendOrderEffectKind = "register_packet_uid"
	EffectSendPacket                   SendOrderEffectKind = "send_packet"
	EffectPacketData                   SendOrderEffectKind = "packet_data"
	EffectEncryptPacketDataPrefixed    SendOrderEffectKind = "encrypt_packet_data_prefixed"
	EffectEncryptPacketDataPassthrough SendOrderEffectKind = "encrypt_packet_data_passthrough"
	EffectSocketWrite                  SendOrderEffectKind = "socket_write_timeout_minus_one"
	EffectEnableOutSegmentTimeout      SendOrderEffectKind = "enable_out_segment_timeout"
	EffectForwardCompletion            SendOrderEffectKind = "forward_completion"
	EffectNoCompletionCallback         SendOrderEffectKind = "no_completion_callback"
	EffectNoSend                       SendOrderEffectKind = "no_send"
	EffectNoReceiveTimeoutArm          SendOrderEffectKind = "no_receive_timeout_arm"
	EffectArmReceiveHeaderTimeout      SendOrderEffectKind = "arm_receive_header_timeout"
)

// SendOrderInput is the source-backed branch input. PacketTag is the unsigned
// packet identifier preserved in the signed timeout argument domain. UniqueID
// remains caller-provided because its construction is a separate contract.
type SendOrderInput struct {
	ProducerStatus    uint8
	CompletionPresent bool
	CryptoPresent     bool
	PacketTag         uint32
	UniqueID          string
}

// SendOrderEffect is a planned effect, not an instruction to perform I/O.
// Tag is populated only by tag-bearing effects. ErrorPresent deliberately
// carries no domain/code because the producer error identity is untraced.
type SendOrderEffect struct {
	Kind          SendOrderEffectKind
	Tag           int64
	UniqueID      string
	PacketPresent bool
	ErrorPresent  bool
	Timeout       int
}

func sendOrderEffect(kind SendOrderEffectKind) SendOrderEffect {
	return SendOrderEffect{Kind: kind}
}

// PlanSendOrder returns the reviewed producer/send ordering without binding a
// socket, crypto implementation, timeout owner, or completion callback.
func PlanSendOrder(input SendOrderInput) []SendOrderEffect {
	if input.ProducerStatus != 3 {
		if !input.CompletionPresent {
			return []SendOrderEffect{
				sendOrderEffect(EffectNoCompletionCallback),
				sendOrderEffect(EffectNoSend),
				sendOrderEffect(EffectNoReceiveTimeoutArm),
			}
		}
		return []SendOrderEffect{
			{Kind: EffectForwardCompletion, PacketPresent: false, ErrorPresent: true},
			sendOrderEffect(EffectNoReceiveTimeoutArm),
		}
	}

	effects := []SendOrderEffect{
		sendOrderEffect(EffectAllocatePacket),
		{Kind: EffectDerivePacketTag, Tag: int64(input.PacketTag)},
	}
	if input.CompletionPresent {
		effects = append(effects,
			SendOrderEffect{Kind: EffectRegisterCompletionByUID, UniqueID: input.UniqueID, Tag: int64(input.PacketTag)},
			SendOrderEffect{Kind: EffectRegisterPacketUID, UniqueID: input.UniqueID, Tag: int64(input.PacketTag)},
		)
	}
	effects = append(effects,
		sendOrderEffect(EffectSendPacket),
		sendOrderEffect(EffectPacketData),
	)
	if input.CryptoPresent {
		effects = append(effects, sendOrderEffect(EffectEncryptPacketDataPrefixed))
	} else {
		effects = append(effects, sendOrderEffect(EffectEncryptPacketDataPassthrough))
	}
	effects = append(effects,
		SendOrderEffect{Kind: EffectSocketWrite, Tag: int64(input.PacketTag), Timeout: -1},
		sendOrderEffect(EffectEnableOutSegmentTimeout),
		SendOrderEffect{Kind: EffectArmReceiveHeaderTimeout, Tag: int64(input.PacketTag)},
	)
	return effects
}
