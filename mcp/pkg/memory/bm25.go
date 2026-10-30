// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchResult pairs a MemoryItem with its relevance score.
type SearchResult struct {
	Item  *MemoryItem
	Score float64
}

// Tokenize splits text into lowercased alphanumeric tokens.
func Tokenize(text string) []string {
	var tokens []string
	var sb strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			if sb.Len() > 0 {
				tokens = append(tokens, sb.String())
				sb.Reset()
			}
		}
	}
	if sb.Len() > 0 {
		tokens = append(tokens, sb.String())
	}
	return tokens
}

// BM25Index performs in-memory BM25 scoring over a collection of MemoryItems.
type BM25Index struct {
	k1 float64
	b  float64
}

// NewBM25Index creates a new BM25 index with standard default parameters (k1=1.2, b=0.75).
func NewBM25Index() *BM25Index {
	return &BM25Index{
		k1: 1.2,
		b:  0.75,
	}
}

// Search ranks memory items by relevance to the query.
// If query is empty, items are returned in their original order with score 0.0.
func (idx *BM25Index) Search(items []*MemoryItem, query string) []SearchResult {
	if len(items) == 0 {
		return nil
	}

	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		results := make([]SearchResult, len(items))
		for i, item := range items {
			results[i] = SearchResult{Item: item, Score: 0.0}
		}
		return results
	}

	// Calculate document term frequencies and lengths
	N := float64(len(items))
	docTFs := make([]map[string]float64, len(items))
	docLens := make([]float64, len(items))
	totalLen := 0.0

	// Document term frequency map for IDF calculation
	docFreqs := make(map[string]int)

	for i, item := range items {
		tf := make(map[string]float64)
		docLen := 0.0

		addWeightedTokens := func(text string, weight float64) {
			tokens := Tokenize(text)
			for _, t := range tokens {
				tf[t] += weight
				docLen += weight
			}
		}

		addWeightedTokens(item.Title, 2.0)
		addWeightedTokens(strings.Join(item.Tags, " "), 2.0)
		addWeightedTokens(item.Tier1Abstract, 2.0)
		addWeightedTokens(item.Tier2Summary, 1.5)
		addWeightedTokens(item.Body, 1.0)

		docTFs[i] = tf
		docLens[i] = docLen
		totalLen += docLen

		for term := range tf {
			docFreqs[term]++
		}
	}

	avgDocLen := totalLen / N
	if avgDocLen == 0 {
		avgDocLen = 1.0
	}

	// Calculate scores
	results := make([]SearchResult, len(items))
	for i, item := range items {
		score := 0.0
		tfMap := docTFs[i]
		docLen := docLens[i]

		for _, q := range queryTokens {
			freq, exists := tfMap[q]
			if !exists || freq == 0 {
				continue
			}

			nq := float64(docFreqs[q])
			// Standard Okapi BM25 IDF
			idf := math.Log((N - nq + 0.5) / (nq + 0.5) + 1.0)
			if idf < 0 {
				idf = 0.01 // Small positive floor for non-negative scoring
			}

			num := freq * (idx.k1 + 1.0)
			denom := freq + idx.k1*(1.0-idx.b+idx.b*(docLen/avgDocLen))

			score += idf * (num / denom)
		}

		results[i] = SearchResult{
			Item:  item,
			Score: score,
		}
	}

	// Sort results descending by score, breaking ties by timestamp descending
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Item.Timestamp > results[j].Item.Timestamp
	})

	return results
}
