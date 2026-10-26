package provider

import "errors"

type NativeToolState interface {
	LoadCodexNativeTools() (map[string]string, error)
	SaveCodexNativeTools(string, string) error
}

func (p *CodexLive) nativeCatalogKnown(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nativeCatalogs[id] == nativeReferenceCatalog
}
func (p *CodexLive) rememberNativeCatalog(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nativeCatalogs == nil {
		p.nativeCatalogs = map[string]string{}
	}
	p.nativeCatalogs[id] = nativeReferenceCatalog
}
func (p *CodexLive) persistNativeCatalogs(ids []string) error {
	if p.nativeToolState == nil {
		return nil
	}
	p.catalogMu.Lock()
	defer p.catalogMu.Unlock()
	for _, id := range ids {
		p.mu.Lock()
		known := p.nativeCatalogs[id] == nativeReferenceCatalog
		done := p.persistedCatalogs[id]
		p.mu.Unlock()
		if !known || done {
			continue
		}
		if err := p.nativeToolState.SaveCodexNativeTools(id, nativeReferenceCatalog); err != nil {
			return errors.New("原生工具能力记录保存失败，下次恢复需重新确认：" + err.Error())
		}
		p.mu.Lock()
		if p.persistedCatalogs == nil {
			p.persistedCatalogs = map[string]bool{}
		}
		p.persistedCatalogs[id] = true
		p.mu.Unlock()
	}
	return nil
}
