package skiplist

import (
	"math/rand"
)

type node struct{
	key string
	value []byte
	forward []*node

}

type Skiplist struct {
	head *node
	maxLevel int
	currTopLevel int
	length int
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
		for current.forward[i] != nil && current.forward[i].key < key {
			current = current.forward[i]
		}
		update[i] = current
	}

	next := update[0].forward[0]

	if next != nil && next.key == key {
		next.value = value
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
		key: key,
		value: value,
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
			current.forward[level].key < key {

			current = current.forward[level]
		}
	}

	current = current.forward[0]

	if current != nil && current.key == key {
		return current, true
	}

	return nil, false
}