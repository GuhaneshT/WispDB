package skiplist

import (
	"math/rand"
)

type node struct{
	Key string
	Value []byte
	forward []*node

}

type Iterator struct {
	current *node
}

func (s *Skiplist) Iterator() *Iterator {
	return &Iterator{
		current: s.head.forward[0],
	}
}

func (it *Iterator) Valid() bool {
	return it.current != nil
}

func (it *Iterator) Next() {
	if it.current != nil {
		it.current = it.current.forward[0]
	}
}

func (it *Iterator) Key() string {
	if it.current == nil {
		return ""
	}
	return it.current.Key
}

func (it *Iterator) Value() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.Value
}

func (it *Iterator) Entry() (string, []byte) {
	if it.current == nil {
		return "", nil
	}
	return it.current.Key, it.current.Value
}

type Skiplist struct {
	head *node
	maxLevel int
	currTopLevel int
	length int
	comparator func(a, b string) int
}

func generateRandomLevel(maxLevel int) int {
	level := 1

	for rand.Float32() < 0.5 && level < maxLevel {
		level++
	}
	return level
}

func NewSkipList(maxLevel int) *Skiplist {
    return &Skiplist{
        head: &node{
            forward: make([]*node, maxLevel),
        },
        maxLevel:    maxLevel,
        currTopLevel: 1,
    }
}

func (s *Skiplist) Insert(key string, value []byte){
	current := s.head
	update := make([]*node, s.maxLevel)

	for i := s.currTopLevel - 1; i >= 0; i-- {
		for current.forward[i] != nil && s.comparator(current.forward[i].Key, key) < 0 {
			current = current.forward[i]
		}
		update[i] = current
	}

	next := update[0].forward[0]

	if next != nil && next.Key == key {
		next.Value = value
		return
	}

	randomLevel := generateRandomLevel(s.maxLevel)
	if randomLevel > s.currTopLevel {
		for i := s.currTopLevel; i < randomLevel; i++ {
			update[i] = s.head
		}
		s.currTopLevel = randomLevel

	}

	newNode := &node{
		Key: key,
		Value: value,
		forward: make([]*node, randomLevel),
	}

	for i:=0; i<randomLevel; i++ {
		newNode.forward[i] = update[i].forward[i]
		update[i].forward[i] = newNode
	}

	s.length++
}


func (s *Skiplist) Search(key string) (*node,bool){
	current := s.head

	for level := s.currTopLevel - 1; level >= 0; level-- {

		for current.forward[level] != nil &&
			s.comparator(current.forward[level].Key, key) < 0 {

			current = current.forward[level]
		}
	}

	current = current.forward[0]

	if current != nil && current.Key == key {
		return current, true
	}

	return nil, false
}

//tbd add generics for future use.

