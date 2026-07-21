//go:build windows

package edge

import (
	"unsafe"
)

type _ICoreWebView2GetCookiesCompletedHandlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}

type iCoreWebView2GetCookiesCompletedHandler struct {
	vtbl *_ICoreWebView2GetCookiesCompletedHandlerVtbl
	impl _ICoreWebView2GetCookiesCompletedHandlerImpl
}

func (i *iCoreWebView2GetCookiesCompletedHandler) AddRef() uint32 {
	ret, _, _ := i.vtbl.AddRef.Call(uintptr(unsafe.Pointer(i)))
	return uint32(ret)
}

func (i *iCoreWebView2GetCookiesCompletedHandler) Release() uint32 {
	ret, _, _ := i.vtbl.Release.Call(uintptr(unsafe.Pointer(i)))
	return uint32(ret)
}

func _ICoreWebView2GetCookiesCompletedHandlerIUnknownQueryInterface(this *iCoreWebView2GetCookiesCompletedHandler, refiid, object uintptr) uintptr {
	return this.impl.QueryInterface(refiid, object)
}

func _ICoreWebView2GetCookiesCompletedHandlerIUnknownAddRef(this *iCoreWebView2GetCookiesCompletedHandler) uintptr {
	return this.impl.AddRef()
}

func _ICoreWebView2GetCookiesCompletedHandlerIUnknownRelease(this *iCoreWebView2GetCookiesCompletedHandler) uintptr {
	return this.impl.Release()
}

func _ICoreWebView2GetCookiesCompletedHandlerInvoke(this *iCoreWebView2GetCookiesCompletedHandler, errorCode uintptr, result *ICoreWebView2CookieList) uintptr {
	return this.impl.GetCookiesCompleted(errorCode, result)
}

type _ICoreWebView2GetCookiesCompletedHandlerImpl interface {
	_IUnknownImpl
	GetCookiesCompleted(errorCode uintptr, result *ICoreWebView2CookieList) uintptr
}

var _ICoreWebView2GetCookiesCompletedHandlerFn = _ICoreWebView2GetCookiesCompletedHandlerVtbl{
	_IUnknownVtbl{
		NewComProc(_ICoreWebView2GetCookiesCompletedHandlerIUnknownQueryInterface),
		NewComProc(_ICoreWebView2GetCookiesCompletedHandlerIUnknownAddRef),
		NewComProc(_ICoreWebView2GetCookiesCompletedHandlerIUnknownRelease),
	},
	NewComProc(_ICoreWebView2GetCookiesCompletedHandlerInvoke),
}

func newICoreWebView2GetCookiesCompletedHandler(impl _ICoreWebView2GetCookiesCompletedHandlerImpl) *iCoreWebView2GetCookiesCompletedHandler {
	return &iCoreWebView2GetCookiesCompletedHandler{
		vtbl: &_ICoreWebView2GetCookiesCompletedHandlerFn,
		impl: impl,
	}
}
