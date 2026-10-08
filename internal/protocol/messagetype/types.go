// Package messagetype names Kakao chat message values recovered from official clients.
// A named value does not imply payload or bridge support. See research/message-types.md.
package messagetype

// Values are the Android 26.8.2 ChatMessageType enum's explicit integers,
// not its ordinal positions. Names follow that enum (Undefined was UNDEFINED).
const (
	Undefined                    int32 = -999999
	Category                     int32 = -100
	ChannelAiNoticeFeed          int32 = -22
	OpenChatMarketWelcomeFeed    int32 = -21
	OpenChatMarketWelcomeMessage int32 = -20
	PartnerECommerceNoticeFeed   int32 = -19
	PartnerStoreNoticeFeed       int32 = -18
	OpenLinkV1SeparatorFeed      int32 = -16
	ChatLogStoreNoticeFeed       int32 = -15
	ECommerceNoticeFeed          int32 = -14
	OpenLinkIllegalBlind         int32 = -13
	DeletedAll                   int32 = -11
	AlimtalkSpamFeed             int32 = -10
	PNCFeed                      int32 = -9
	SecretChatInSecureFeed       int32 = -7
	SecretChatWelcomeFeed        int32 = -6
	LostChatLogsFeed             int32 = -5
	SpamFeed                     int32 = -4
	LastRead                     int32 = -3
	KakaoLink                    int32 = -2
	TimeLine                     int32 = -1
	Feed                         int32 = 0
	Text                         int32 = 1
	Photo                        int32 = 2
	Video                        int32 = 3
	Contact                      int32 = 4
	Audio                        int32 = 5
	AnimatedEmoticon             int32 = 6
	DigitalItemGift              int32 = 7
	Link                         int32 = 9
	OldLocation                  int32 = 10
	Avatar                       int32 = 11
	Sticker                      int32 = 12
	Schedule                     int32 = 13
	Vote                         int32 = 14
	CJ20121212                   int32 = 15
	Location                     int32 = 16
	Profile                      int32 = 17
	File                         int32 = 18
	AnimatedSticker              int32 = 20
	Nudge                        int32 = 21
	Spritecon                    int32 = 22
	SharpSearch                  int32 = 23
	Post                         int32 = 24
	AnimatedStickerEx            int32 = 25
	Reply                        int32 = 26
	MultiPhoto                   int32 = 27
	LargeVideo                   int32 = 28
	LargeFile                    int32 = 29
	Mvoip                        int32 = 51
	VoxRoom                      int32 = 52
	Leverage                     int32 = 71
	Alimtalk                     int32 = 72
	PlusLeverage                 int32 = 73
	Plus                         int32 = 81
	PlusEvent                    int32 = 82
	PlusViral                    int32 = 83
	ScheduleForOpenLink          int32 = 96
	VoteForOpenLink              int32 = 97
	PostForOpenLink              int32 = 98
	Universal                    int32 = 100
	UniversalVerified            int32 = 101
)

// Flags are separate from enum values. Do not strip them to route a message as
// an ordinary supported type: their semantics are not implemented by mooo.
const (
	DeletedAllChatTypeFlag   int32 = 0x00004000 // DELETED_ALL_CHAT_TYPE
	OpenLinkIllegalBlindFlag int32 = 0x00008000 // OPENLINK_ILLEGAL_BLIND
	SecretChatTypeFlag       int32 = 0x10000000 // SECRET_CHAT_TYPE
)
