package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jdkato/prose/v2"
	"gopkg.in/yaml.v2"
)

// Configuration structures
type OutputConfig struct {
	IncludePhonetic bool `yaml:"includePhonetic"`
	IncludeOrigin   bool `yaml:"includeOrigin"`
	IncludeSynonyms bool `yaml:"includeSynonyms"`
	IncludeAntonyms bool `yaml:"includeAntonyms"`
	FilterNoExample bool `yaml:"filterDefinitionsWithoutExamples"`
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
var proxyConfig ProxyConfig
var wordCache = make(map[string]WordCache)
var cachePath = "word_cache.json"
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

// Configuration loading
func loadConfig() OutputConfig {
	defaultConfig := OutputConfig{
		IncludePhonetic: true,
		IncludeOrigin:   true,
		IncludeSynonyms: true,
		IncludeAntonyms: true,
		FilterNoExample: false,
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
	cachedData, exists := wordCache[word]

	if !exists {
		apiURL := fmt.Sprintf("https://api.dictionaryapi.dev/api/v2/entries/en/%s", word)

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}

		req.Header.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Add("Accept", "application/json")

		client := createHTTPClient()
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			return fmt.Sprintf("%s\n\tNo details available.\n", capitalizePhrase(word))
		}
		defer resp.Body.Close()

		bodyBytes, _ := ioutil.ReadAll(resp.Body)

		var result []map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &result); err != nil || len(result) == 0 {
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
		if phonetics, ok := result[0]["phonetics"].([]interface{}); ok {
			for _, p := range phonetics {
				if phoneticMap, ok := p.(map[string]interface{}); ok {
					if text, ok := phoneticMap["text"].(string); ok && text != "" {
						cachedData.Phonetic = text
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

		// Save to cache
		wordCache[strings.ToLower(word)] = cachedData
		saveWordCache()
	}

	// Format output
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%s\n", capitalizePhrase(word)))

	if config.IncludePhonetic && cachedData.Phonetic != "" {
		output.WriteString(fmt.Sprintf("\tPhonetic: %s\n", cachedData.Phonetic))
	}

	if config.IncludeOrigin && cachedData.Origin != "" {
		output.WriteString(fmt.Sprintf("\tOrigin: %s\n", cachedData.Origin))
	}

	if len(cachedData.Definitions) == 0 {
		output.WriteString("\tNo details available.\n")
		return output.String()
	}

	for i, def := range cachedData.Definitions {
		if config.FilterNoExample && def.Example == "" {
			continue
		}

		output.WriteString(fmt.Sprintf("\t%d. (%s) %s\n", i+1, def.PartOfSpeech, def.Definition))

		if def.Example != "" {
			output.WriteString(fmt.Sprintf("\t   Example: %s\n", def.Example))
		}

		if config.IncludeSynonyms && len(def.Synonyms) > 0 {
			output.WriteString(fmt.Sprintf("\t   Synonyms: %s\n", strings.Join(def.Synonyms, ", ")))
		}

		if config.IncludeAntonyms && len(def.Antonyms) > 0 {
			output.WriteString(fmt.Sprintf("\t   Antonyms: %s\n", strings.Join(def.Antonyms, ", ")))
		}
	}

	return output.String()
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
	// Create output directory
	outputDir := "outputs"
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
	for category, file := range outputFiles {
		explanationFiles[category] = strings.Replace(file, ".txt", "_ex.txt", 1)
	}

	// Get all unique words and sort by frequency
	sortedAllWords := sortByFrequency(allWordsDict)
	totalUniqueWords := len(sortedAllWords)

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
		exFilePath := explanationFiles[category]

		// Create files
		wordFile, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create output file for %s: %v", category, err)
		}
		defer wordFile.Close()

		exFile, err := os.Create(exFilePath)
		if err != nil {
			return fmt.Errorf("failed to create explanation file for %s: %v", category, err)
		}
		defer exFile.Close()

		wordWriter := bufio.NewWriter(wordFile)
		exWriter := bufio.NewWriter(exFile)

		log.Printf("\nProcessing %s category (%d words):\n", category, len(sortedWords))
		fmt.Printf("\nProcessing %s category (%d words):\n", category, len(sortedWords))

		for i, word := range sortedWords {
			wordWriter.WriteString(capitalizePhrase(word) + "\n")
			printProgress(
				fmt.Sprintf("Dictionary lookup (%s)", category),
				word,
				i+1,
				len(sortedWords))
			exWriter.WriteString(fetchWordDetails(word) + "\n")
		}

		wordWriter.Flush()
		exWriter.Flush()
		log.Printf("\n- Category '%s' processed: %d words\n", category, len(sortedWords))
		fmt.Printf("\n- Category '%s' processed: %d words\n", category, len(sortedWords))
	}

	log.Println("\nGenerating final outputs...")
	fmt.Println("\nGenerating final outputs...")

	// Write AllWords files
	allWordsPath := filepath.Join(outputDir, "AllWords.txt")
	allWordsExPath := filepath.Join(outputDir, "AllWords_ex.txt")

	allWordsFile, err := os.Create(allWordsPath)
	if err != nil {
		return fmt.Errorf("failed to create AllWords.txt file: %v", err)
	}
	defer allWordsFile.Close()

	allWordsExFile, err := os.Create(allWordsExPath)
	if err != nil {
		return fmt.Errorf("failed to create AllWords_ex.txt file: %v", err)
	}
	defer allWordsExFile.Close()

	allWordsWriter := bufio.NewWriter(allWordsFile)
	allWordsExWriter := bufio.NewWriter(allWordsExFile)

	for i, word := range sortedAllWords {
		allWordsWriter.WriteString(capitalizePhrase(word) + "\n")
		printProgress("Processing All Words explanations", word, i+1, totalUniqueWords)
		allWordsExWriter.WriteString(fetchWordDetails(word) + "\n")
	}

	allWordsWriter.Flush()
	allWordsExWriter.Flush()
	log.Println("\n- AllWords.txt complete")
	fmt.Println("\n- AllWords.txt complete")
	log.Println("- AllWords_ex.txt complete")
	fmt.Println("- AllWords_ex.txt complete")

	// Report results
	log.Printf("\n===== Analysis Results =====\n")
	log.Printf("Total unique words after deduplication: %d\n", totalUniqueWords)
	log.Printf("Results written to directory: %s\n", outputDir)

	fmt.Printf("\n===== Analysis Results =====\n")
	fmt.Printf("Total unique words after deduplication: %d\n", totalUniqueWords)
	fmt.Printf("Results written to directory: %s\n", outputDir)

	return nil
}

func main() {
	// Setup logging
	setupLogging()
	defer logFile.Close()

	log.Println("Application started")

	// Load configuration and proxy settings
	config = loadConfig()
	proxyConfig = loadProxyConfig()
	loadWordCache()

	// Process all files in the "inputs" directory
	inputDir := "inputs"

	// Create inputs directory if it doesn't exist
	if _, err := os.Stat(inputDir); os.IsNotExist(err) {
		if err := os.MkdirAll(inputDir, os.ModePerm); err != nil {
			log.Fatalf("Failed to create inputs directory: %v", err)
			fmt.Printf("Failed to create inputs directory: %v\n", err)
			return
		}
		log.Println("Created inputs directory. Please place text files there and run the program again.")
		fmt.Println("Created inputs directory. Please place text files there and run the program again.")
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
