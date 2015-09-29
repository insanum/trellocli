
package main

import (
    "time"
    "encoding/json"
    "flag"
    "fmt"
    "io/ioutil"
    "log"
    "os"
    "github.com/mrjones/oauth"
    "github.com/nsf/termbox-go"
)

var debug bool = false

var trello_url      string = "https://api.trello.com/1"
var oauth_file_name string = ".trello_oauth.json"

type OauthData struct {
    ApiKey      string
    AccessToken oauth.AccessToken
}

var oauth_data OauthData
var consumer   *oauth.Consumer

type TrelloCard struct {
    card map[string]interface{}
}

type TrelloList struct {
    list          map[string]interface{}
    cards_fetched bool
    cards         []*TrelloCard
}

type TrelloBoard struct {
    board         map[string]interface{}
    lists_fetched bool
    lists         []*TrelloList
}

var all_boards []*TrelloBoard

type CursorPosition struct {
    line    int
    column  int
}

const (
    PageTypeAllBoards int = iota
    PageTypeBoard
    PageTypeList
    PageTypeCard
    PageTypeHelp
)

type Page struct {
    // if this struct changes, update push_page_on_stack()
    cursor     CursorPosition
    cells      []termbox.Cell
    ptype      int // PageType
    start_line int
    board      *TrelloBoard
    list       *TrelloList
    card       *TrelloCard
}

var current_page *Page
var page_stack   []*Page

func (page *Page) set_cursor(line     int,
                             column   int,
                             absolute bool) {
    if absolute {
        page.cursor.line   = line
        page.cursor.column = column
    } else {
        page.cursor.line   += line
        page.cursor.column += column
    }
    //termbox.SetCursor(cursor.line, cursor.column)
    termbox.HideCursor()
}

func dump_json(json_data interface{}) {
    data_indent, err := json.MarshalIndent(json_data, "", "    ")
    if err != nil {
        log.Fatal(err)
    }

    os.Stdout.Write(data_indent)
    fmt.Print("\n")
}

func FileExists(name string) bool {
    _, err := os.Stat(name)
    return !os.IsNotExist(err)
}

func do_oauth() {
    var oauth_file = os.ExpandEnv("$HOME") + "/" + oauth_file_name
    var request_token_url string = "https://trello.com/1/OAuthGetRequestToken"
    var authorize_url string = "https://trello.com/1/OAuthAuthorizeToken"
    var access_token_url string = "https://trello.com/1/OAuthGetAccessToken"

    if FileExists(oauth_file) {
        oauth_json_data, err := ioutil.ReadFile(oauth_file)
        if err != nil {
            log.Fatal(err)
        }

        err = json.Unmarshal(oauth_json_data, &oauth_data)
        if err != nil {
            log.Fatal(err)
        }

        if ((oauth_data.ApiKey == "") ||
            (oauth_data.AccessToken.Token == "") ||
            (oauth_data.AccessToken.Secret == "")) {
            log.Fatal("Invalid oauth data!")
        }

        consumer = oauth.NewConsumer(oauth_data.ApiKey,
                                     oauth_data.AccessToken.Secret,
                                     oauth.ServiceProvider{})
        if consumer == nil {
            log.Fatal("Invalid consumer")
        }

        return
    }

    fmt.Print("Go here to get your keys: https://trello.com/app-key\n")

    fmt.Print("Your Trello API key: ")
    api_key := ""
    fmt.Scanln(&api_key)

    fmt.Print("Your Trello API secret: ")
    api_secret := ""
    fmt.Scanln(&api_secret)

    fmt.Print("Expiration [30days]: ")
    expiration := ""
    fmt.Scanln(&expiration)
    if expiration == "" {
        expiration = "30days"
    }

    scope := "read,write"

    c := oauth.NewConsumer(
        api_key,
        api_secret,
        oauth.ServiceProvider{
            RequestTokenUrl:   request_token_url,
            AuthorizeTokenUrl: authorize_url,
            AccessTokenUrl:    access_token_url,
        })

    c.AdditionalAuthorizationUrlParams = map[string]string{
        "name":       "Trello CLI",
        "scope":      scope,
        "expiration": expiration,
    }

    //c.Debug(true)

    requestToken, url, err := c.GetRequestTokenAndUrl("")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Print("Go here to perform the authorization:\n")
    fmt.Print(url, "\n")

    fmt.Print("Did you accept the authorization? [y|n] ")
    yes_no := ""
    fmt.Scanln(&yes_no)
    if (yes_no != "y") && (yes_no != "Y") &&
        (yes_no != "yes") && (yes_no != "YES") &&
        (yes_no != "Yes") {
        return
    }

    fmt.Print("Verification code: ")
    verificationCode := ""
    fmt.Scanln(&verificationCode)

    accessToken, err := c.AuthorizeToken(requestToken, verificationCode)
    if err != nil {
        log.Fatal(err)
    }

    oauth_data.ApiKey = api_key
    oauth_data.AccessToken = *accessToken

    oauth_json_data, err := json.Marshal(oauth_data)
    if err != nil {
        log.Fatal(err)
    }

    err = ioutil.WriteFile(oauth_file, oauth_json_data, 0600)
    if err != nil {
        log.Fatal(err)
    }

    consumer = oauth.NewConsumer(oauth_data.ApiKey,
                                 oauth_data.AccessToken.Secret,
                                 oauth.ServiceProvider{})
    if consumer == nil {
        log.Fatal("Invalid consumer")
    }
}

func trello_get(url string) interface{} {
    var data interface{}
    response, err := consumer.Get(url, map[string]string{}, &oauth_data.AccessToken)
    if err != nil {
        log.Fatal(err)
    }

    defer response.Body.Close()

    body, err := ioutil.ReadAll(response.Body)
    if err != nil {
        log.Fatal(err)
    }

    json.Unmarshal(body, &data)

    return data
}

func get_list_cards(tl *TrelloList) {
    var url string = trello_url + "/lists/" + tl.list["id"].(string) + "/cards"
    var cards []interface{}

    cards = trello_get(url).([]interface{})
    for _, c := range cards {
        if c.(map[string]interface{})["closed"] == true {
            continue
        }

        tc := new(TrelloCard)
        tc.card = c.(map[string]interface{})

        tl.cards = append(tl.cards, tc)

        if debug {
            fmt.Printf("        %s\n", tc.card["name"])
        }
    }
}

func get_board_lists(recurse bool,
                     tb *TrelloBoard) {
    var url string = trello_url + "/boards/" + tb.board["id"].(string) + "/lists"
    var lists []interface{}

    lists = trello_get(url).([]interface{})
    for _, l := range lists {
        if l.(map[string]interface{})["closed"] == true {
            continue
        }

        tl := new(TrelloList)
        tl.list          = l.(map[string]interface{})
        tl.cards_fetched = false
        tl.cards         = make([]*TrelloCard, 0)

        tb.lists = append(tb.lists, tl)

        if debug {
            fmt.Printf("    %s\n", tl.list["name"])
        }

        if recurse == true {
            get_list_cards(tl)
            tl.cards_fetched = true
        }
    }
}

func get_boards(recurse bool) {
    var url string = trello_url + "/members/me/boards"
    var boards []interface{}

    all_boards = make([]*TrelloBoard, 0)

    boards = trello_get(url).([]interface{})
    for _, b := range boards {
        if b.(map[string]interface{})["closed"] == true {
            continue
        }

        tb := new(TrelloBoard)
        tb.board         = b.(map[string]interface{})
        tb.lists_fetched = false
        tb.lists         = make([]*TrelloList, 0)

        all_boards = append(all_boards, tb)

        if debug {
            fmt.Printf("%s\n", tb.board["name"])
        }

        if recurse == true {
            get_board_lists(recurse, tb)
            tb.lists_fetched = true
        }
    }
}

type board_func func(tb *TrelloBoard)
type list_func  func(tb *TrelloBoard, tl *TrelloList)
type card_func  func(tb *TrelloBoard, tl *TrelloList, tc *TrelloCard)

func iter_boards(fn board_func) {
    for _, b := range all_boards {
        fn(b)
    }
}

func iter_lists(tb *TrelloBoard,
                fn list_func) {
    for _, l := range tb.lists {
        fn(tb, l)
    }
}

func iter_cards(tb *TrelloBoard,
                tl *TrelloList,
                fn card_func) {
    for _, c := range tl.cards {
        fn(tb, tl, c)
    }
}

func print_board_info(tb *TrelloBoard) {
    fmt.Printf("[B] %s -> %s\n",
               tb.board["name"], tb.board["shortUrl"])
}

func print_list_info(tb *TrelloBoard,
                     tl *TrelloList) {
    fmt.Printf("[L] %s -> %s\n",
               tb.board["name"], tl.list["name"])
}

func print_card_info(tb *TrelloBoard,
                     tl *TrelloList,
                     tc *TrelloCard) {
    fmt.Printf("[C] %s -> %s -> %s -> %s\n",
               tb.board["name"], tl.list["name"], tc.card["name"], tc.card["shortUrl"])
}

func recurse_boards() {
    for _, tb := range all_boards {
        print_board_info(tb)
        for _, tl := range tb.lists {
            print_list_info(tb, tl)
            for _, tc := range tl.cards {
                print_card_info(tb, tl, tc)
            }
        }
    }
}

func tbprint(x   int,
             y   int,
             fg  termbox.Attribute,
             bg  termbox.Attribute,
             msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
        if c == '\n' {
            x = 0
            y++
        } else {
            x++
        }
	}
}

func tbprint_reverse(x   int,
                     y   int,
                     fg  termbox.Attribute,
                     bg  termbox.Attribute,
                     msg string) {
    for i := len(msg)-1; i >= 0; i-- {
		termbox.SetCell(x, y, rune(msg[i]), fg, bg)
		x--
	}
}

func tbprint_width(y   int,
                   fg  termbox.Attribute,
                   bg  termbox.Attribute,
                   msg string) {
	w, _ := termbox.Size()
    x := 0
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
    for x < w {
		termbox.SetCell(x, y, ' ', fg, bg)
		x++
    }
}

func tbprint_width_reverse(y   int,
                           fg  termbox.Attribute,
                           bg  termbox.Attribute,
                           msg string) {
	w, _ := termbox.Size()
    x := w-1
    for i := len(msg)-1; i >= 0; i-- {
		termbox.SetCell(x, y, rune(msg[i]), fg, bg)
		x--
	}
    for x >= 0 {
		termbox.SetCell(x, y, ' ', fg, bg)
		x--
    }
}

func idx_str(cur_idx  int,
             num_elem int) (string) {
    return fmt.Sprintf("(%d/%d)", cur_idx, num_elem)
}

func clear_screen() {
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)
}

func draw_screen() {
	termbox.Flush()
}

func alloc_new_page(ptype int) (*Page) {
    page := new(Page)
    page.ptype = ptype
    switch ptype {
    case PageTypeAllBoards:
        page.start_line = 1
    case PageTypeBoard:
        page.start_line = 1
    case PageTypeList:
        page.start_line = 2
    case PageTypeCard:
        page.start_line = 3
    case PageTypeHelp:
        page.start_line = 1
    }
    page.board = nil
    page.list  = nil
    page.card  = nil
    return page
}

func draw_all_boards_page() {
    // first page shown... page stack is empty
    // this page will be pushed on the stack when the page changes

    page := alloc_new_page(PageTypeAllBoards)
    page.set_cursor(page.start_line, 0, true)

    current_page = page

    clear_screen()
	//w, _ := termbox.Size()

    title := fmt.Sprintf("BOARDS")
    tbprint_width(0,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    /*
    tbprint_reverse(w-1, 0,
                    termbox.ColorDefault,
                    termbox.ColorDefault | termbox.AttrReverse,
                    idx_str(1, len(all_boards)))
    */

    v := 1
    for i, tb := range all_boards {
        fg := termbox.ColorDefault
        bg := termbox.ColorDefault
        if i == 0 {
            fg = termbox.ColorYellow
            bg = termbox.ColorDefault | termbox.AttrBold
        }
        tbprint(0, v, fg, bg, tb.board["name"].(string))
        v++
    }

    draw_screen()
}

func print_message(msg string) {
    _, h := termbox.Size()
    tbprint_width(h-1,
                  termbox.ColorYellow,
                  termbox.ColorBlack | termbox.AttrReverse,
                  msg)
}

func draw_new_board_page() {
    push_page_on_stack()
    clear_screen()

    page := alloc_new_page(PageTypeBoard)
    page.board = all_boards[current_page.cursor.line-current_page.start_line]
    page.set_cursor(page.start_line, 0, true)

    current_page = page

    if page.board.lists_fetched == false {
        get_board_lists(false, page.board)
        page.board.lists_fetched = true
    }

	//w, _ := termbox.Size()

    title := fmt.Sprintf("BOARD: %s", page.board.board["name"])
    tbprint_width(0,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    /*
    tbprint_reverse(w-1, 0,
                    termbox.ColorDefault,
                    termbox.ColorDefault | termbox.AttrReverse,
                    idx_str(1, len(page.board.lists)))
    */

    v := 1
    for i, tl := range page.board.lists {
        fg := termbox.ColorDefault
        bg := termbox.ColorDefault
        if i == 0 {
            fg = termbox.ColorYellow
            bg = termbox.ColorDefault | termbox.AttrBold
        }
        tbprint(0, v, fg, bg, tl.list["name"].(string))
        v++
    }

    draw_screen()
}

func draw_new_list_page() {
    push_page_on_stack()
    clear_screen()

    page := alloc_new_page(PageTypeList)
    page.board = current_page.board
    page.list  = current_page.board.lists[current_page.cursor.line-current_page.start_line]
    page.set_cursor(page.start_line, 0, true)

    current_page = page

    if page.list.cards_fetched == false {
        get_list_cards(page.list)
        page.list.cards_fetched = true
    }


	//w, _ := termbox.Size()

    title := fmt.Sprintf("BOARD: %s", page.board.board["name"])
    tbprint_width(0,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    title = fmt.Sprintf("  LIST: %s", page.list.list["name"])
    tbprint_width(1,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    /*
    tbprint_reverse(w-1, 0,
                    termbox.ColorDefault,
                    termbox.ColorDefault | termbox.AttrReverse,
                    idx_str(1, len(page.list.cards)))
    */

    v := 2
    for i, tc := range page.list.cards {
        fg := termbox.ColorDefault
        bg := termbox.ColorDefault
        if i == 0 {
            fg = termbox.ColorYellow
            bg = termbox.ColorDefault | termbox.AttrBold
        }
        tbprint(0, v, fg, bg, tc.card["name"].(string))
        v++
    }

    draw_screen()
}

func draw_new_card_page() {
    push_page_on_stack()
    clear_screen()

    page := alloc_new_page(PageTypeCard)
    page.board = current_page.board
    page.list  = current_page.list
    page.card  = current_page.list.cards[current_page.cursor.line-current_page.start_line]
    page.set_cursor(page.start_line, 0, true)

    current_page = page

    title := fmt.Sprintf("BOARD: %s", page.board.board["name"])
    tbprint_width(0,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    title = fmt.Sprintf("  LIST: %s", page.list.list["name"])
    tbprint_width(1,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    title = fmt.Sprintf("    CARD: %s", page.card.card["name"])
    tbprint_width(2,
                  termbox.ColorDefault,
                  termbox.ColorDefault | termbox.AttrReverse,
                  title)

    v := 3

    fg := termbox.ColorDefault
    bg := termbox.ColorDefault

    tbprint(0, v, fg, bg, page.card.card["desc"].(string))

    // XXX get checklists

    draw_screen()
}

func draw_new_page() {
    switch current_page.ptype {
    case PageTypeAllBoards:
        draw_new_board_page()
    case PageTypeBoard:
        draw_new_list_page()
    case PageTypeList:
        draw_new_card_page()
    default:
        return
    }
}

func set_line_attr(line int,
                   fg termbox.Attribute,
                   bg termbox.Attribute) {
	w, _ := termbox.Size()

    cells := termbox.CellBuffer()

    for i := 0; i < w; i++ {
        cell := (line * w) + i
        cells[cell].Fg = fg
        cells[cell].Bg = bg
    }
}

func move_cursor(dir int) {
    new_line := current_page.cursor.line+dir

    set_line_attr(current_page.cursor.line,
                  termbox.ColorDefault,
                  termbox.ColorDefault)
    if current_page.ptype != PageTypeCard {
        set_line_attr(new_line,
                      termbox.ColorYellow,
                      termbox.ColorDefault | termbox.AttrBold)
    } else {
        set_line_attr(new_line,
                      termbox.ColorDefault,
                      termbox.ColorDefault)
    }

    current_page.set_cursor(new_line, current_page.cursor.column, true)

    draw_screen()
}

func move_cursor_down() {
    var max_len int
    ok := true

    switch current_page.ptype {
    case PageTypeAllBoards:
        max_len = len(all_boards)
    case PageTypeBoard:
        max_len = len(current_page.board.lists)
    case PageTypeList:
        max_len = len(current_page.list.cards)
    case PageTypeCard:
        max_len = 50 // XXX
    case PageTypeHelp:
        max_len = 50 // XXX
    default:
        ok = false
    }

    max_len += (current_page.start_line - 1)

    if ok && current_page.cursor.line < max_len {
        move_cursor(1)
    }
}

func move_cursor_up() {
    if current_page.cursor.line > current_page.start_line {
        move_cursor(-1)
    }
}

func push_page_on_stack() {
    page := termbox.CellBuffer()
    copy_page := new(Page)
    copy_page.cursor     = current_page.cursor
    copy_page.cells      = make([]termbox.Cell, len(page))
    copy_page.ptype      = current_page.ptype
    copy_page.start_line = current_page.start_line
    copy_page.board      = current_page.board
    copy_page.list       = current_page.list
    copy_page.card       = current_page.card

    for i, c := range page {
        copy_page.cells[i] = c
    }

    page_stack = append(page_stack, copy_page)
}

func pop_page_from_stack() {
    var page *Page
    page, page_stack = page_stack[len(page_stack)-1], page_stack[:len(page_stack)-1]

	w, _ := termbox.Size()

    clear_screen()

    for i, c := range page.cells {
        termbox.SetCell(i%w, i/w, c.Ch, c.Fg, c.Bg)
    }

    current_page = page

    draw_screen()
}

func main() {
    flag.Parse()

    do_oauth()

    err := termbox.Init()
    if err != nil {
        log.Fatal(err)
    }

    defer termbox.Close()
    termbox.SetInputMode(termbox.InputEsc)

    get_boards(false)

    page_stack = make([]*Page, 0)

    draw_all_boards_page()
    time.Sleep(1 * time.Second)

mainloop:
    for {
        ev := termbox.PollEvent()
        switch ev.Type {
        case termbox.EventKey:
            switch ev.Key {
            /*
            case termbox.KeyEsc:
                break mainloop
            */
            case termbox.KeyEnter:
                draw_new_page()
            default:
                switch ev.Ch {
                case 'Q':
                    break mainloop
                case 'q':
                    if len(page_stack) == 0 {
                        break mainloop
                    } else {
                        pop_page_from_stack()
                    }
                case 'j':
                    move_cursor_down()
                case 'k':
                    move_cursor_up()
                }
            }
        case termbox.EventError:
            log.Fatal(ev.Err)
        }
    }
}

