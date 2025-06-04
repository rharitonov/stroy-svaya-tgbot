package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"stroy-svaya-tgbot/internal/model"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

const (
	WebServiceURL = "https://localhost:8080"
	DateFormat    = "02.01.2006"
	GroupsCount   = 6
)

type UserState struct {
	WaitingFor       string
	CurrentRecord    model.PileDrivingRecordLine
	AvailablePiles   []string
	SelectionHistory [][]string
}

var (
	bot        *tgbotapi.BotAPI
	userStates = make(map[int64]*UserState)
)

func main() {
	var err error
	err = godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	tg_token := os.Getenv("TG_TOKEN")

	bot, err = tgbotapi.NewBotAPI(tg_token)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		text := update.Message.Text

		if _, ok := userStates[chatID]; !ok {
			userStates[chatID] = &UserState{}
		}

		state := userStates[chatID]

		switch text {
		case "/start":
			sendMessage(chatID, "Добро пожаловать в журнал забивки свай!\n\n"+
				"Используйте команды:\n"+
				"/newrecord - начать новую запись\n"+
				"/help - помощь")
		case "/help":
			sendMessage(chatID, "Команды бота:\n"+
				"/newrecord - начать новую запись о забивке сваи\n"+
				"/help - показать эту справку")
		case "/newrecord":
			startNewRecord(chatID, state)
		default:
			processUserInput(chatID, state, text)
		}
	}
}

func startNewRecord(chatID int64, state *UserState) {
	state.CurrentRecord = model.PileDrivingRecordLine{}
	state.SelectionHistory = [][]string{}
	state.AvailablePiles = getPilesToDriving()
	if len(state.AvailablePiles) == 0 {
		sendMessage(chatID, "Нет доступных свай для забивки.")
		return
	}
	state.WaitingFor = "pileNumber"
	showPileGroups(chatID, state, state.AvailablePiles)
}

func getPilesToDriving() []string {
	url := fmt.Sprintf("%s/getpilestodriving?project_id=1", WebServiceURL)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err.Error())
	}
	var piles []string
	if err := json.Unmarshal(body, &piles); err != nil {
		log.Fatal(err.Error())
	}
	return piles
}

func showPileGroups(chatID int64, state *UserState, piles []string) {
	if len(piles) <= GroupsCount {
		// Показываем отдельные сваи
		showSinglePiles(chatID, state, piles)
		return
	}

	groups := splitIntoGroups(piles, GroupsCount)

	// Сохраняем текущие группы в историю выбора
	state.SelectionHistory = append(state.SelectionHistory, groups...)

	// Создаем клавиатуру с группами
	rows := make([][]tgbotapi.KeyboardButton, 0, GroupsCount)
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		minPile := group[0]
		maxPile := group[len(group)-1]
		btn := tgbotapi.NewKeyboardButton(fmt.Sprintf("%s..%s", minPile, maxPile))
		rows = append(rows, tgbotapi.NewKeyboardButtonRow(btn))
	}

	msg := tgbotapi.NewMessage(chatID, "Выберите группу свай:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(rows...)
	_, err := bot.Send(msg)
	if err != nil {
		log.Println("Ошибка при отправке сообщения:", err)
	}
}

func showSinglePiles(chatID int64, state *UserState, piles []string) {
	rows := make([][]tgbotapi.KeyboardButton, 0, len(piles))
	for _, pile := range piles {
		btn := tgbotapi.NewKeyboardButton(pile)
		rows = append(rows, tgbotapi.NewKeyboardButtonRow(btn))
	}

	msg := tgbotapi.NewMessage(chatID, "Выберите номер сваи:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(rows...)
	_, err := bot.Send(msg)
	if err != nil {
		log.Println("Ошибка при отправке сообщения:", err)
	}
}

func extractNumberFromPile(pile string) int {
	// Извлекаем числовую часть из номера сваи (например, "СВ-12" -> 12)
	parts := strings.Split(pile, "-")
	if len(parts) < 2 {
		return 0
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return num
}

func splitIntoGroups(piles []string, groupsCount int) [][]string {
	if len(piles) <= groupsCount {
		// Если свай меньше или равно количеству групп, возвращаем каждую сваю отдельно
		result := make([][]string, len(piles))
		for i, pile := range piles {
			result[i] = []string{pile}
		}
		return result
	}

	// Делим сваи на группы
	groupSize := len(piles) / groupsCount
	remainder := len(piles) % groupsCount

	groups := make([][]string, groupsCount)
	start := 0
	for i := 0; i < groupsCount; i++ {
		end := start + groupSize
		if i < remainder {
			end++
		}
		groups[i] = piles[start:end]
		start = end
	}

	return groups
}

func processUserInput(chatID int64, state *UserState, text string) {
	switch state.WaitingFor {
	case "pileNumber":
		handlePileNumberSelection(chatID, state, text)
	case "drivingDate":
		handleDrivingDateSelection(chatID, state, text)
	case "pileTopLevel":
		handlePileTopLevelInput(chatID, state, text)
	case "operatorName":
		handleOperatorNameInput(chatID, state, text)
	default:
		sendMessage(chatID, "Используйте /newrecord для начала новой записи или /help для справки.")
	}
}

func handlePileNumberSelection(chatID int64, state *UserState, text string) {
	if strings.Contains(text, "..") {
		// Пользователь выбрал группу
		selectedGroup := findSelectedGroup(state.SelectionHistory, text)
		if selectedGroup == nil {
			sendMessage(chatID, "Неверный выбор группы. Попробуйте еще раз.")
			return
		}
		showPileGroups(chatID, state, selectedGroup)
	} else {
		// Пользователь выбрал конкретную сваю
		if !contains(state.AvailablePiles, text) {
			sendMessage(chatID, "Неверный номер сваи. Пожалуйста, выберите из предложенных вариантов.")
			return
		}

		state.CurrentRecord.PileNumber = text
		state.WaitingFor = "drivingDate"
		sendDateSelection(chatID)
	}
}

func findSelectedGroup(history [][]string, selection string) []string {
	if len(history) == 0 {
		return nil
	}
	for _, group := range history {
		if len(group) == 0 {
			continue
		}
		minPile := group[0]
		maxPile := group[len(group)-1]
		if fmt.Sprintf("%s..%s", minPile, maxPile) == selection {
			return group
		}
	}
	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func sendDateSelection(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "Выберите дату забивки:")

	keyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("Сегодня"),
			tgbotapi.NewKeyboardButton("Вчера"),
		),
	)

	msg.ReplyMarkup = keyboard
	_, err := bot.Send(msg)
	if err != nil {
		log.Println("Ошибка при отправке сообщения:", err)
	}
}

func handleDrivingDateSelection(chatID int64, state *UserState, text string) {
	now := time.Now()
	var selectedDate time.Time

	switch text {
	case "Сегодня":
		selectedDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "Вчера":
		selectedDate = time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	default:
		sendMessage(chatID, "Пожалуйста, выберите одну из предложенных дат")
		sendDateSelection(chatID)
		return
	}

	state.CurrentRecord.StartDate = selectedDate
	state.WaitingFor = "pileTopLevel"
	sendMessage(chatID, fmt.Sprintf("Выбрана дата: %s\nВведите отметку верха головы сваи (в милиметрах, например, 12750):",
		selectedDate.Format(DateFormat)))
}

func handlePileTopLevelInput(chatID int64, state *UserState, text string) {
	factPileHead, err := parseInt(text)
	if err != nil {
		sendMessage(chatID, "Неверный формат числа. Пожалуйста, введите отметку в милиметрах (например, 12750):")
		return
	}
	state.CurrentRecord.FactPileHead = factPileHead
	state.WaitingFor = "operatorName"
	sendMessage(chatID, "Введите имя оператора (или /skip чтобы пропустить):")
}

func handleOperatorNameInput(chatID int64, state *UserState, text string) {
	if text != "/skip" {
		state.CurrentRecord.RecordedBy = text
	}
	state.WaitingFor = "additionalInfo"
	sendMessage(chatID, "Введите дополнительную информацию (или /skip чтобы пропустить):")
}

func sendDataToWebService(chatID int64, state *UserState) {
	jsonData, err := json.Marshal(state.CurrentRecord)
	if err != nil {
		sendMessage(chatID, "Ошибка при подготовке данных: "+err.Error())
		return
	}

	resp, err := http.Post(WebServiceURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		sendMessage(chatID, "Ошибка при отправке данных на сервер: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Убираем клавиатуру после отправки
	msg := tgbotapi.NewMessage(chatID, "")
	msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)

	if resp.StatusCode == http.StatusOK {
		msg.Text = "Данные успешно отправлены!\n\n" +
			"Номер сваи: " + state.CurrentRecord.PileNumber + "\n" +
			"Дата забивки: " + state.CurrentRecord.StartDate.Format(DateFormat) + "\n" +
			"Отметка верха: " + fmt.Sprintf("%.2f", state.CurrentRecord.FactPileHead) + " м"
	} else {
		msg.Text = "Сервер вернул ошибку: " + resp.Status
	}

	_, err = bot.Send(msg)
	if err != nil {
		log.Println("Ошибка при отправке сообщения:", err)
	}

	// Сбрасываем состояние пользователя
	state.WaitingFor = ""
	state.SelectionHistory = [][]string{}
}

func sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := bot.Send(msg)
	if err != nil {
		log.Println("Ошибка при отправке сообщения:", err)
	}
}

func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}
