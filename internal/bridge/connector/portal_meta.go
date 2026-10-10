package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
)

// savePortalMeta saves a mutated copy of the portal's metadata and re-reads
// the stored portal so that verify can confirm the change is durable. The
// shared metadata value is never modified in place, and the previous value is
// restored if the save itself fails.
func (kc *KakaoClient) savePortalMeta(ctx context.Context, p *bridgev2.Portal, mutate func(*KakaoPortalMetadata), verify func(*KakaoPortalMetadata) bool) error {
	meta, ok := p.Metadata.(*KakaoPortalMetadata)
	if !ok || meta == nil {
		return errors.New("connector: portal metadata is unavailable")
	}
	next := *meta
	mutate(&next)
	p.Metadata = &next
	if err := p.Save(ctx); err != nil {
		p.Metadata = meta
		return err
	}
	saved, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, p.PortalKey)
	if err != nil {
		return err
	}
	if saved == nil {
		return errors.New("connector: portal disappeared while saving its metadata")
	}
	got, ok := saved.Metadata.(*KakaoPortalMetadata)
	if !ok || got == nil || !verify(got) {
		return errors.New("connector: portal metadata was not durable")
	}
	return nil
}
