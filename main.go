package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"github.com/jdkato/prose/v2"
	"gopkg.in/yaml.v2"
)

// InputConfig structure for input directory configuration
type InputConfig struct {
	InputDirectory string `yaml:"inputDirectory"`
}

// Configuration structures
type OutputConfig struct {
	IncludePhonetic          bool `yaml:"includePhonetic"`
	IncludeOrigin            bool `yaml:"includeOrigin"`
	IncludeSynonyms          bool `yaml:"includeSynonyms"`
	IncludeAntonyms          bool `yaml:"includeAntonyms"`
	FilterNoExample          bool `yaml:"filterDefinitionsWithoutExamples"`
	GenerateExplanations     bool `yaml:"generateExplanations"`     // Toggle for explanation files
	GenerateExampleSentences bool `yaml:"generateExampleSentences"` // Toggle for example sentences files
	MaxExampleSentences      int  `yaml:"maxExampleSentences"`      // Maximum number of example sentences per word
}

type QueryConfig struct {
	QueryForUnknownWords bool `yaml:"queryForUnknownWords"` // Whether to query words marked as unknown
}

type ProxyConfig struct {
	HTTPProxy  string `yaml:"httpProxy"`
	HTTPSProxy string `yaml:"httpsProxy"`
}

type Definition struct {
	PartOfSpeech string
	Definition   string
	Example      string
	Synonyms     []string
	Antonyms     []string
}

type WordCache struct {
	Definitions []Definition
	Phonetic    string
	Origin      string
	Synonyms    []string
	Antonyms    []string
}

// Global variables
var config OutputConfig
var queryConfig QueryConfig
var proxyConfig ProxyConfig
var inputConfig InputConfig
var wordCache = make(map[string]WordCache)
var wordUnknown = make(map[string]bool)
var cachePath = "word_cache.json"
var unknownPath = "word_unknown.json"
var logFile *os.File

// Helper functions
func isEnglishText(text string) bool {
	for _, r := range text {
		if !unicode.IsLetter(r) && r != ' ' && r != '-' && r != '/' {
			return false
		}
		if !unicode.In(r, unicode.Latin) {
			return false
		}
	}
	return true
}

func capitalizePhrase(phrase string) string {
	words := strings.Fields(phrase)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(string(word[0])) + strings.ToLower(word[1:])
		}
	}
	return strings.Join(words, " ")
}

func capitalizeSentence(sentence string) string {
	if len(sentence) == 0 {
		return ""
	}
	return strings.ToUpper(string(sentence[0])) + sentence[1:]
}

func splitSlashSeparatedWords(text string) []string {
	parts := strings.Split(text, "/")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}

func countFrequencies(content []string) map[string]int {
	counts := make(map[string]int)
	for _, item := range content {
		counts[capitalizePhrase(item)]++
	}
	return counts
}

func sortByFrequency(counts map[string]int) []string {
	type itemFreq struct {
		Item string
		Freq int
	}
	var items []itemFreq
	for item, freq := range counts {
		items = append(items, itemFreq{Item: item, Freq: freq})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Freq > items[j].Freq
	})
	var result []string
	for _, item := range items {
		result = append(result, item.Item)
	}
	return result
}

// Remove empty lines from the text
func removeEmptyLines(text string) string {
	lines := strings.Split(text, "\n")
	var nonEmptyLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}
	return strings.Join(nonEmptyLines, "\n")
}

// Deduplicate a slice of strings
func deduplicateStrings(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// Configuration loading
func loadConfig() OutputConfig {
	defaultConfig := OutputConfig{
		IncludePhonetic:          true,
		IncludeOrigin:            true,
		IncludeSynonyms:          true,
		IncludeAntonyms:          true,
		FilterNoExample:          false,
		GenerateExplanations:     true, // Default to true for backward compatibility
		GenerateExampleSentences: true, // Default to true for example sentences files
		MaxExampleSentences:      0,    // Default to 0 meaning no limit
	}

	configPath := "outputConfig.yml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		yamlData, _ := yaml.Marshal(defaultConfig)
		ioutil.WriteFile(configPath, yamlData, 0644)
		return defaultConfig
	}

	yamlFile, err := ioutil.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}

	var config OutputConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return defaultConfig
	}
	return config
}

func loadQueryConfig() QueryConfig {
	defaultConfig := QueryConfig{
		QueryForUnknownWords: false, // Default to not query unknown words
	}

	configPath := "queryConfig.yml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		yamlData, _ := yaml.Marshal(defaultConfig)
		ioutil.WriteFile(configPath, yamlData, 0644)
		return defaultConfig
	}

	yamlFile, err := ioutil.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}

	var config QueryConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return defaultConfig
	}
	return config
}

func loadProxyConfig() ProxyConfig {
	defaultConfig := ProxyConfig{
		HTTPProxy:  "",
		HTTPSProxy: "",
	}

	configPath := "proxy.yml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		yamlData, _ := yaml.Marshal(defaultConfig)
		ioutil.WriteFile(configPath, yamlData, 0644)
		return defaultConfig
	}

	yamlFile, err := ioutil.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}

	var config ProxyConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return defaultConfig
	}
	return config
}

// Load input directory configuration
func loadInputConfig() InputConfig {
	defaultConfig := InputConfig{
		InputDirectory: "inputs",
	}

	configPath := "inputConfig.yml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		yamlData, _ := yaml.Marshal(defaultConfig)
		ioutil.WriteFile(configPath, yamlData, 0644)
		return defaultConfig
	}

	yamlFile, err := ioutil.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}

	var config InputConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return defaultConfig
	}
	return config
}

// Check if directory exists and is valid
func isValidDirectory(path string) bool {
	if path == "" {
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.IsDir()
}

// Show directory selection dialog
func selectDirectoryGUI() (string, error) {
	selectedDir := ""
	done := make(chan struct{})

	// Initialize Fyne application
	a := app.New()
	w := a.NewWindow("Select Input Directory")

	// Show directory open dialog
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			log.Printf("Error selecting directory: %v\n", err)
			selectedDir = ""
		} else if uri == nil {
			// User canceled
			selectedDir = ""
		} else {
			selectedDir = uri.Path()
		}
		w.Close()
		close(done)
	}, w)

	// Show and run window
	w.Resize(fyne.NewSize(800, 600))
	w.Show()

	// Wait for directory selection to complete
	go func() {
		a.Run()
	}()

	<-done

	if selectedDir == "" {
		return "", fmt.Errorf("no directory selected")
	}

	return selectedDir, nil
}

// Cache management
func loadWordCache() {
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return
	}

	data, err := ioutil.ReadFile(cachePath)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &wordCache); err != nil {
		wordCache = make(map[string]WordCache)
	}
}

func saveWordCache() {
	data, err := json.MarshalIndent(wordCache, "", "  ")
	if err != nil {
		return
	}
	ioutil.WriteFile(cachePath, data, 0644)
}

// Load unknown words
func loadWordUnknown() {
	if _, err := os.Stat(unknownPath); os.IsNotExist(err) {
		return
	}

	data, err := ioutil.ReadFile(unknownPath)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &wordUnknown); err != nil {
		wordUnknown = make(map[string]bool)
	}
}

func saveWordUnknown() {
	data, err := json.MarshalIndent(wordUnknown, "", "  ")
	if err != nil {
		return
	}
	ioutil.WriteFile(unknownPath, data, 0644)
}

func createHTTPClient() *http.Client {
	transport := &http.Transport{}

	if proxyConfig.HTTPSProxy != "" {
		proxyURL, err := url.Parse(proxyConfig.HTTPSProxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else if proxyConfig.HTTPProxy != "" {
		proxyURL, err := url.Parse(proxyConfig.HTTPProxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}
}

func fetchWordDetails(word string) string {
	word = strings.ToLower(word)

	// Check if the word is in the unknown words database
	if _, isUnknown := wordUnknown[word]; isUnknown {
		// If configured not to query unknown words, return empty result
		if !queryConfig.QueryForUnknownWords {
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}
		// Otherwise, proceed with the query as normal
	}

	// Check if the word is in the cache
	cachedData, exists := wordCache[word]

	if !exists {
		apiURL := fmt.Sprintf("https://api.dictionaryapi.dev/api/v2/entries/en/%s", word)

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			// Add to unknown words
			wordUnknown[word] = true
			saveWordUnknown()
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}

		req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Add("Accept", "application/json")

		client := createHTTPClient()
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			// Add to unknown words
			wordUnknown[word] = true
			saveWordUnknown()
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}
		defer resp.Body.Close()

		bodyBytes, _ := ioutil.ReadAll(resp.Body)

		var result []map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &result); err != nil || len(result) == 0 {
			// Add to unknown words
			wordUnknown[word] = true
			saveWordUnknown()
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}

		// Process API response into our cache structure
		cachedData = WordCache{
			Definitions: []Definition{},
			Phonetic:    "",
			Origin:      "",
			Synonyms:    []string{},
			Antonyms:    []string{},
		}

		// Extract phonetic if available
		if phonetic, ok := result[0]["phonetic"].(string); ok {
			cachedData.Phonetic = phonetic
		}

		// Extract phonetics
		if phonetics, ok := result[0]["phonetics"].([]interface{}); ok && cachedData.Phonetic == "" {
			for _, p := range phonetics {
				if phoneticMap, ok := p.(map[string]interface{}); ok {
					if text, ok := phoneticMap["text"].(string); ok && text != "" {
						cachedData.Phonetic = text
						break
					}
				}
			}
		}

		// Extract origin directly from the top level
		if originStr, ok := result[0]["origin"].(string); ok {
			cachedData.Origin = originStr
		}

		// Extract meanings, definitions, synonyms, antonyms
		if meanings, ok := result[0]["meanings"].([]interface{}); ok {
			for _, m := range meanings {
				if meaningMap, ok := m.(map[string]interface{}); ok {
					partOfSpeech := ""
					if pos, ok := meaningMap["partOfSpeech"].(string); ok {
						partOfSpeech = pos
					}

					// Extract definitions
					if definitions, ok := meaningMap["definitions"].([]interface{}); ok {
						for _, d := range definitions {
							defMap, ok := d.(map[string]interface{})
							if !ok {
								continue
							}

							def := Definition{
								PartOfSpeech: partOfSpeech,
								Definition:   "",
								Example:      "",
								Synonyms:     []string{},
								Antonyms:     []string{},
							}

							if defStr, ok := defMap["definition"].(string); ok {
								def.Definition = defStr
							}

							if exampleStr, ok := defMap["example"].(string); ok {
								def.Example = exampleStr
							}

							// Extract synonyms and antonyms
							if syns, ok := defMap["synonyms"].([]interface{}); ok {
								for _, syn := range syns {
									if synStr, ok := syn.(string); ok {
										def.Synonyms = append(def.Synonyms, synStr)
										cachedData.Synonyms = append(cachedData.Synonyms, synStr)
									}
								}
							}

							if ants, ok := defMap["antonyms"].([]interface{}); ok {
								for _, ant := range ants {
									if antStr, ok := ant.(string); ok {
										def.Antonyms = append(def.Antonyms, antStr)
										cachedData.Antonyms = append(cachedData.Antonyms, antStr)
									}
								}
							}

							cachedData.Definitions = append(cachedData.Definitions, def)
						}
					}
				}
			}
		}

		// If definitions were found, save to cache and remove from unknown words if it was there
		if len(cachedData.Definitions) > 0 {
			wordCache[strings.ToLower(word)] = cachedData
			saveWordCache()

			// If the word was previously unknown, remove it from unknown words
			if _, wasUnknown := wordUnknown[word]; wasUnknown {
				delete(wordUnknown, word)
				saveWordUnknown()
			}
		} else {
			// No definitions found, mark as unknown
			wordUnknown[word] = true
			saveWordUnknown()
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}
	}

	// Format output with the new layout
	var output strings.Builder
	capitalized := capitalizePhrase(word)

	// Put word and phonetic on the same line
	if cachedData.Phonetic != "" && config.IncludePhonetic {
		output.WriteString(fmt.Sprintf("%s %s\n", capitalized, cachedData.Phonetic))
	} else {
		output.WriteString(fmt.Sprintf("%s\n", capitalized))
	}

	// Add origin if available and enabled
	if config.IncludeOrigin && cachedData.Origin != "" {
		output.WriteString(fmt.Sprintf("\tOrigin: %s\n", cachedData.Origin))
	}

	// Check if there are definitions available
	if len(cachedData.Definitions) == 0 {
		output.WriteString(fmt.Sprintf("\t%s: No details available.\n", capitalized))
		return output.String()
	}

	// Process definitions with the new format
	for i, def := range cachedData.Definitions {
		if config.FilterNoExample && def.Example == "" {
			continue
		}

		defNumber := i + 1

		// Write definition with number and word prefix
		output.WriteString(fmt.Sprintf("\t%s %d, %s: %s\n",
			capitalized, defNumber, def.PartOfSpeech, def.Definition))

		// Add example if available, with word and number prefix
		if def.Example != "" {
			output.WriteString(fmt.Sprintf("\t\t%s %d Example: %s\n",
				capitalized, defNumber, def.Example))
		}

		// Add synonyms if enabled and available, with word and number prefix
		if config.IncludeSynonyms && len(def.Synonyms) > 0 {
			output.WriteString(fmt.Sprintf("\t\t%s %d Synonyms: %s\n",
				capitalized, defNumber, strings.Join(def.Synonyms, ", ")))
		}

		// Add antonyms if enabled and available, with word and number prefix
		if config.IncludeAntonyms && len(def.Antonyms) > 0 {
			output.WriteString(fmt.Sprintf("\t\t%s %d Antonyms: %s\n",
				capitalized, defNumber, strings.Join(def.Antonyms, ", ")))
		}
	}

	return removeEmptyLines(output.String())
}

// Check if a word has details
func hasWordDetails(word string) bool {
	word = strings.ToLower(word)

	// Check if the word is in the unknown words database
	if _, isUnknown := wordUnknown[word]; isUnknown {
		return false
	}

	// Check if the word is in the cache and has definitions
	if cachedData, exists := wordCache[word]; exists {
		return len(cachedData.Definitions) > 0
	}

	return false
}

// Function to generate example sentences file for a word
func generateExampleSentencesContent(word string) string {
	word = strings.ToLower(word)

	// Skip if word is in unknown words
	if _, isUnknown := wordUnknown[word]; isUnknown {
		return ""
	}

	cachedData, exists := wordCache[word]

	if !exists || len(cachedData.Definitions) == 0 {
		return ""
	}

	var output strings.Builder
	capitalized := capitalizePhrase(word)
	output.WriteString(capitalized + "\n")

	// Collect all examples first
	var examples []string
	for _, def := range cachedData.Definitions {
		if def.Example != "" {
			// Make sure the first letter is capitalized
			example := capitalizeSentence(def.Example)
			examples = append(examples, example)
		}
	}

	if len(examples) == 0 {
		return ""
	}

	// Apply max example sentence limit if configured
	maxExamples := config.MaxExampleSentences
	totalExamples := len(examples)

	// If maxExamples is 0 or greater than or equal to total examples, use all examples
	if maxExamples == 0 || maxExamples >= totalExamples {
		for _, example := range examples {
			output.WriteString("\t" + example + "\n")
		}
	} else {
		// Randomly select maxExamples unique examples
		// Create a copy of the examples slice to avoid modifying the original
		examplesCopy := make([]string, len(examples))
		copy(examplesCopy, examples)

		// Initialize random seed
		rand.Seed(time.Now().UnixNano())

		// Select maxExamples unique examples
		selectedExamples := make([]string, 0, maxExamples)
		for i := 0; i < maxExamples; i++ {
			// Generate random index
			randIndex := rand.Intn(len(examplesCopy))
			// Add the example at the random index to selected examples
			selectedExamples = append(selectedExamples, examplesCopy[randIndex])
			// Remove the selected example to avoid duplicates
			examplesCopy = append(examplesCopy[:randIndex], examplesCopy[randIndex+1:]...)
		}

		// Write selected examples to output
		for _, example := range selectedExamples {
			output.WriteString("\t" + example + "\n")
		}
	}

	return removeEmptyLines(output.String())
}

func printProgress(stage string, item string, current, total int) {
	percentage := int((float64(current) / float64(total)) * 100)
	fmt.Printf("\r%-80s", " ") // Clear line
	fmt.Printf("\r%s: %s (%d of %d) - %d%%", stage, capitalizePhrase(item), current, total, percentage)
}

func setupLogging() {
	var err error
	logFile, err = os.OpenFile("log.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
		log.SetFlags(log.LstdFlags)
	}
}

// Read and process a single file, returning the categorized words and all words
func processFile(inputFile string) (map[string][]string, map[string]int, error) {
	// Read input file
	file, err := os.Open(inputFile)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var content string
	for scanner.Scan() {
		content += scanner.Text() + " "
	}

	// Create NLP document
	doc, err := prose.NewDocument(content)
	if err != nil {
		return nil, nil, err
	}

	categorizedWords := map[string][]string{}
	allWords := map[string]int{}

	// Process tokens
	tokens := doc.Tokens()
	totalTokens := len(tokens)
	log.Printf("Processing file: %s (%d tokens)\n", inputFile, totalTokens)
	fmt.Printf("Processing file: %s (%d tokens)\n", inputFile, totalTokens)

	for i, tok := range tokens {
		text := strings.ToLower(tok.Text)
		printProgress("Classifying text", text, i+1, totalTokens)

		// Process slash-separated words
		wordParts := splitSlashSeparatedWords(text)
		for _, part := range wordParts {
			if isEnglishText(part) {
				allWords[part]++
				var category string
				switch tok.Tag {
				case "NN", "NNS", "NNP", "NNPS":
					category = "Nouns"
				case "VB", "VBD", "VBP", "VBZ", "VBG":
					category = "Verbs"
				case "JJ", "JJR", "JJS":
					category = "Adjectives"
				case "RB", "RBR", "RBS":
					category = "Adverbs"
				default:
					category = "OtherWords"
				}
				categorizedWords[category] = append(categorizedWords[category], part)
			}
		}
	}

	return categorizedWords, allWords, nil
}

// Process all files in the input directory
func processAllFiles(inputDir string) error {
	// Create output directory based on input directory name
	inputDirName := filepath.Base(inputDir)
	outputDir := inputDirName + "_ewClassifiers"
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Get all .txt files from input directory
	files, err := ioutil.ReadDir(inputDir)
	if err != nil {
		return fmt.Errorf("failed to read input directory: %v", err)
	}

	var txtFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(strings.ToLower(file.Name()), ".txt") {
			txtFiles = append(txtFiles, filepath.Join(inputDir, file.Name()))
		}
	}

	if len(txtFiles) == 0 {
		return fmt.Errorf("no text files found in input directory")
	}

	log.Printf("Found %d text files to process\n", len(txtFiles))
	fmt.Printf("Found %d text files to process\n", len(txtFiles))

	// Initialize maps to collect words from all files
	allCategorizedWords := map[string][]string{
		"Nouns":      {},
		"Verbs":      {},
		"Adjectives": {},
		"Adverbs":    {},
		"OtherWords": {},
	}
	allWordsDict := make(map[string]int)

	// Process each file
	for _, inputFile := range txtFiles {
		log.Printf("Processing file: %s\n", inputFile)
		fmt.Printf("Processing file: %s\n", inputFile)

		categorizedWords, fileWords, err := processFile(inputFile)
		if err != nil {
			log.Printf("Error processing file %s: %v\n", inputFile, err)
			fmt.Printf("Error processing file %s: %v\n", inputFile, err)
			continue
		}

		// Merge words into collection
		for category, words := range categorizedWords {
			allCategorizedWords[category] = append(allCategorizedWords[category], words...)
		}

		for word, count := range fileWords {
			allWordsDict[word] += count
		}

		log.Printf("Finished processing file: %s\n", inputFile)
		fmt.Printf("Finished processing file: %s\n", inputFile)
	}

	log.Println("\nProcessing complete. Starting dictionary lookups...")
	fmt.Println("\nProcessing complete. Starting dictionary lookups...")

	// Define output file paths
	outputFiles := map[string]string{
		"Nouns":      filepath.Join(outputDir, "Nouns.txt"),
		"Verbs":      filepath.Join(outputDir, "Verbs.txt"),
		"Adjectives": filepath.Join(outputDir, "Adjectives.txt"),
		"Adverbs":    filepath.Join(outputDir, "Adverbs.txt"),
		"OtherWords": filepath.Join(outputDir, "OtherWords.txt"),
	}

	explanationFiles := map[string]string{}
	if config.GenerateExplanations {
		// Only setup explanation files if the toggle is enabled
		for category, file := range outputFiles {
			explanationFiles[category] = strings.Replace(file, ".txt", "_ex.txt", 1)
		}
	}

	exampleSentencesFiles := map[string]string{}
	if config.GenerateExampleSentences {
		// Only setup example sentences files if the toggle is enabled
		for category, file := range outputFiles {
			exampleSentencesFiles[category] = strings.Replace(file, ".txt", "_es.txt", 1)
		}
	}

	// Create a file for unknown words
	unknownWordsPath := filepath.Join(outputDir, "UnknownWords.txt")
	unknownWordsFile, err := os.Create(unknownWordsPath)
	if err != nil {
		return fmt.Errorf("failed to create UnknownWords.txt file: %v", err)
	}
	defer unknownWordsFile.Close()
	unknownWordsWriter := bufio.NewWriter(unknownWordsFile)

	// Get all unique words and sort by frequency
	sortedAllWords := sortByFrequency(allWordsDict)

	// Track unknown words
	var unknownWords []string

	// Write each category to separate files
	for category, words := range allCategorizedWords {
		// Create word frequency map and sort
		freqMap := countFrequencies(words)
		sortedWords := sortByFrequency(freqMap)

		if len(sortedWords) == 0 {
			log.Printf("No words in category: %s\n", category)
			continue
		}

		filePath := outputFiles[category]

		// Create word list file (always created)
		wordFile, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create output file for %s: %v", category, err)
		}
		defer wordFile.Close()
		wordWriter := bufio.NewWriter(wordFile)

		// Only create explanation file if the toggle is enabled
		var exFile *os.File
		var exWriter *bufio.Writer
		if config.GenerateExplanations {
			exFilePath := explanationFiles[category]
			exFile, err = os.Create(exFilePath)
			if err != nil {
				return fmt.Errorf("failed to create explanation file for %s: %v", category, err)
			}
			defer exFile.Close()
			exWriter = bufio.NewWriter(exFile)
		}

		// Only create example sentences file if the toggle is enabled
		var esFile *os.File
		var esWriter *bufio.Writer
		if config.GenerateExampleSentences {
			esFilePath := exampleSentencesFiles[category]
			esFile, err = os.Create(esFilePath)
			if err != nil {
				return fmt.Errorf("failed to create example sentences file for %s: %v", category, err)
			}
			defer esFile.Close()
			esWriter = bufio.NewWriter(esFile)
		}

		log.Printf("\nProcessing %s category (%d words):\n", category, len(sortedWords))
		fmt.Printf("\nProcessing %s category (%d words):\n", category, len(sortedWords))

		// Deduplicate the words
		sortedWords = deduplicateStrings(sortedWords)

		// Process each word
		for i, word := range sortedWords {
			printProgress(
				fmt.Sprintf("Dictionary lookup (%s)", category),
				word,
				i+1,
				len(sortedWords))

			// Fetch word details and check if it's unknown
			wordDetails := fetchWordDetails(word)
			isUnknown := strings.Contains(wordDetails, "No details available.")

			if isUnknown {
				// Add to unknown words list
				unknownWords = append(unknownWords, capitalizePhrase(word))
			} else {
				// Only write known words to the word list file
				wordWriter.WriteString(capitalizePhrase(word) + "\n")

				// Only write to explanation file if toggle is enabled
				if config.GenerateExplanations {
					exWriter.WriteString(wordDetails)
				}

				// Only write to example sentences file if toggle is enabled
				if config.GenerateExampleSentences {
					esContent := generateExampleSentencesContent(word)
					if esContent != "" {
						esWriter.WriteString(esContent)
					}
				}
			}
		}

		wordWriter.Flush()
		if config.GenerateExplanations {
			exWriter.Flush()
		}
		if config.GenerateExampleSentences {
			esWriter.Flush()
		}

		log.Printf("\n- Category '%s' processed: %d words\n", category, len(sortedWords))
		fmt.Printf("\n- Category '%s' processed: %d words\n", category, len(sortedWords))
	}

	log.Println("\nGenerating final outputs...")
	fmt.Println("\nGenerating final outputs...")

	// Always create AllWords.txt file
	allWordsPath := filepath.Join(outputDir, "AllWords.txt")
	allWordsFile, err := os.Create(allWordsPath)
	if err != nil {
		return fmt.Errorf("failed to create AllWords.txt file: %v", err)
	}
	defer allWordsFile.Close()
	allWordsWriter := bufio.NewWriter(allWordsFile)

	// Only create AllWords_ex.txt if toggle is enabled
	var allWordsExFile *os.File
	var allWordsExWriter *bufio.Writer
	if config.GenerateExplanations {
		allWordsExPath := filepath.Join(outputDir, "AllWords_ex.txt")
		allWordsExFile, err = os.Create(allWordsExPath)
		if err != nil {
			return fmt.Errorf("failed to create AllWords_ex.txt file: %v", err)
		}
		defer allWordsExFile.Close()
		allWordsExWriter = bufio.NewWriter(allWordsExFile)
	}

	// Only create AllWords_es.txt if toggle is enabled
	var allWordsEsFile *os.File
	var allWordsEsWriter *bufio.Writer
	if config.GenerateExampleSentences {
		allWordsEsPath := filepath.Join(outputDir, "AllWords_es.txt")
		allWordsEsFile, err = os.Create(allWordsEsPath)
		if err != nil {
			return fmt.Errorf("failed to create AllWords_es.txt file: %v", err)
		}
		defer allWordsEsFile.Close()
		allWordsEsWriter = bufio.NewWriter(allWordsEsFile)
	}

	// Deduplicate unknown words list
	unknownWords = deduplicateStrings(unknownWords)

	// Write unknown words to UnknownWords.txt
	for _, word := range unknownWords {
		unknownWordsWriter.WriteString(word + "\n")
	}
	unknownWordsWriter.Flush()

	// Process all words
	sortedAllWords = deduplicateStrings(sortedAllWords)
	for i, word := range sortedAllWords {
		printProgress("Processing All Words", word, i+1, len(sortedAllWords))

		// Skip unknown words in AllWords.txt and related files
		if !hasWordDetails(word) {
			continue
		}

		allWordsWriter.WriteString(capitalizePhrase(word) + "\n")

		if config.GenerateExplanations {
			allWordsExWriter.WriteString(fetchWordDetails(word))
		}

		if config.GenerateExampleSentences {
			esContent := generateExampleSentencesContent(word)
			if esContent != "" {
				allWordsEsWriter.WriteString(esContent)
			}
		}
	}

	allWordsWriter.Flush()

	if config.GenerateExplanations {
		allWordsExWriter.Flush()
		log.Println("- AllWords_ex.txt complete")
		fmt.Println("- AllWords_ex.txt complete")
	}

	if config.GenerateExampleSentences {
		allWordsEsWriter.Flush()
		log.Println("- AllWords_es.txt complete")
		fmt.Println("- AllWords_es.txt complete")
	}

	log.Println("- AllWords.txt complete")
	fmt.Println("- AllWords.txt complete")
	log.Println("- UnknownWords.txt complete")
	fmt.Println("- UnknownWords.txt complete")

	// Report results
	log.Printf("\n===== Analysis Results =====\n")
	log.Printf("Results written to directory: %s\n", outputDir)
	if config.GenerateExplanations {
		log.Printf("Word explanation files were generated.\n")
	} else {
		log.Printf("Word explanation files were not generated (disabled in config).\n")
	}
	if config.GenerateExampleSentences {
		log.Printf("Example sentences files were generated.\n")
	} else {
		log.Printf("Example sentences files were not generated (disabled in config).\n")
	}

	fmt.Printf("\n===== Analysis Results =====\n")
	fmt.Printf("Results written to directory: %s\n", outputDir)
	if config.GenerateExplanations {
		fmt.Printf("Word explanation files were generated.\n")
	} else {
		fmt.Printf("Word explanation files were not generated (disabled in config).\n")
	}
	if config.GenerateExampleSentences {
		fmt.Printf("Example sentences files were generated.\n")
	} else {
		fmt.Printf("Example sentences files were not generated (disabled in config).\n")
	}

	return nil
}

func main() {
	// Setup logging
	setupLogging()
	defer logFile.Close()

	log.Println("Application started")

	// Load configuration and proxy settings
	config = loadConfig()
	queryConfig = loadQueryConfig()
	proxyConfig = loadProxyConfig()
	loadWordCache()
	loadWordUnknown()

	// Load input directory configuration
	inputConfig = loadInputConfig()

	// Determine input directory
	var inputDir string

	// First check if the input directory is configured in inputConfig.yml
	if isValidDirectory(inputConfig.InputDirectory) {
		log.Printf("Using configured input directory: %s\n", inputConfig.InputDirectory)
		fmt.Printf("Using configured input directory: %s\n", inputConfig.InputDirectory)
		inputDir = inputConfig.InputDirectory
	} else {
		// If not configured or invalid, let user select via GUI
		log.Println("No valid input directory configured, prompting user to select one...")
		fmt.Println("No valid input directory configured, prompting user to select one...")

		selectedDir, err := selectDirectoryGUI()
		if err != nil {
			// Fallback to default "inputs" directory
			inputDir = "inputs"
			log.Printf("Falling back to default input directory: %s\n", inputDir)
			fmt.Printf("Falling back to default input directory: %s\n", inputDir)
		} else {
			inputDir = selectedDir
			log.Printf("Using selected directory: %s\n", inputDir)
			fmt.Printf("Using selected directory: %s\n", inputDir)
		}
	}

	// Create inputs directory if it doesn't exist
	if _, err := os.Stat(inputDir); os.IsNotExist(err) {
		if err := os.MkdirAll(inputDir, os.ModePerm); err != nil {
			log.Fatalf("Failed to create input directory: %v", err)
			fmt.Printf("Failed to create input directory: %v\n", err)
			return
		}
		log.Printf("Created input directory '%s'. Please place text files there and run the program again.\n", inputDir)
		fmt.Printf("Created input directory '%s'. Please place text files there and run the program again.\n", inputDir)
		return
	}

	err := processAllFiles(inputDir)
	if err != nil {
		log.Printf("Error during processing: %v\n", err)
		fmt.Printf("Error during processing: %v\n", err)
		return
	}

	log.Println("Text analysis complete.")
	fmt.Println("Text analysis complete.")
}
