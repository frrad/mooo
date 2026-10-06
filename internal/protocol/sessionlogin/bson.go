package sessionlogin

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// loginListDocument is deliberately separate from the semantic request. The
// reviewed serializer omits nil object-valued properties, including sKey.
// There is no `rp`: the official builder has no such property.
// Fixed-width Go integer types preserve the observed BSON widths.
type loginListDocument struct {
	AppVer      string  `bson:"appVer"`
	OS          string  `bson:"os"`
	Lang        string  `bson:"lang"`
	DUUID       string  `bson:"duuid"`
	OAuthToken  string  `bson:"oauthToken"`
	NType       int32   `bson:"ntype"`
	MCCMNC      string  `bson:"MCCMNC,omitempty"`
	Revision    int32   `bson:"revision"`
	DType       int32   `bson:"dtype"`
	PCST        int32   `bson:"pcst"`
	BG          bool    `bson:"bg"`
	ChatIDs     []int64 `bson:"chatIds"`
	MaxIDs      []int64 `bson:"maxIds"`
	LastTokenID int64   `bson:"lastTokenId"`
	LBK         int32   `bson:"lbk"`
}

func (r LoginListRequest) MarshalBSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	chatIDs := r.ChatIDs
	if chatIDs == nil {
		chatIDs = []int64{}
	}
	maxIDs := r.MaxIDs
	if maxIDs == nil {
		maxIDs = []int64{}
	}
	doc := loginListDocument{
		AppVer: r.AppVer, OS: r.OS, Lang: r.Lang, DUUID: r.DUUID,
		OAuthToken: r.OAuthToken, NType: r.NType, MCCMNC: r.MCCMNC,
		Revision: r.Revision, DType: r.DType, PCST: r.PCST,
		BG: r.BG, ChatIDs: chatIDs, MaxIDs: maxIDs,
		LastTokenID: r.LastTokenID, LBK: r.LBK,
	}
	out, err := bson.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("sessionlogin: encode LOGINLIST: %w", err)
	}
	return out, nil
}
