package skiplist

import (
	"math/rand"
)

type node struct{
	Key Key
	Value []byte
	forward []*node

}

func compare(a, b Key) int {
    if a.SeriesID < b.SeriesID {
        return -1
    }
    if a.SeriesID > b.SeriesID {
        return 1
    }

    if a.Timestamp < b.Timestamp {
        return -1
    }
    if a.Timestamp > b.Timestamp {
        return 1
    }

    return 0
}

type Key struct{
	SeriesID uint64
	Timestamp int64
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

func (it *Iterator) SeriesID() uint64 {
	if it.current == nil {
		return 0
	}
	return it.current.Key.SeriesID	
}

func (it *Iterator) Value() []byte {
	if it.current == nil {
		return nil
	}
	return it.current.Value
}

func (it *Iterator) Entry() (Key, []byte) {
	if it.current == nil {
		return Key{}, nil
	}
	return it.current.Key, it.current.Value
}

type Skiplist struct {
	head *node
	maxLevel int
	currTopLevel int
	length int
	comparator func(a, b Key) int
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
		comparator: compare,
    }
}

func (s *Skiplist) Insert(key Key, value []byte){
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


func (s *Skiplist) Search(key Key) (*node,bool){
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

