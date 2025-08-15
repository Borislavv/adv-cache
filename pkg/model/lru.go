package model

import "github.com/Borislavv/advanced-cache/pkg/list"

func (e *Entry) SetLruListElement(el *list.Element[*Entry]) { e.lruListElem.Store(el) }
func (e *Entry) LruListElement() *list.Element[*Entry]      { return e.lruListElem.Load() }
