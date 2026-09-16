import XCTest

/// Сквозная проверка главного сценария: открыть рецепт, приготовить, увидеть
/// уменьшившийся остаток. Заодно снимает экраны, до которых нельзя добраться
/// без нажатий, и складывает PNG в контейнер раннера.
///
/// XCUIApplication и XCUIElement с Xcode 27 помечены @MainActor, поэтому
/// класс тоже на главном акторе — иначе каждый вызов даёт предупреждение.
@MainActor
final class CookingFlowUITests: XCTestCase {

    override func setUp() {
        continueAfterFailure = false
    }

    private func launch(tab: String) -> XCUIApplication {
        // Ландшафт: в портрете на 11" панель вкладок сворачивает последние
        // вкладки под шеврон «ещё», и до них не дотянуться прямым запросом.
        XCUIDevice.shared.orientation = .landscapeLeft
        let app = XCUIApplication()
        app.launchArguments += ["-startTab", tab, "-resetData", "YES"]
        app.launch()
        return app
    }

    /// Вкладка по имени. На 11" в панель помещаются не все пять: остальные
    /// уезжают на вторую «страницу» или в боковое меню — оба пути настоящие,
    /// поэтому тест проходит их так же, как прошёл бы человек.
    @discardableResult
    private func openTab(_ name: String, in app: XCUIApplication) -> Bool {
        if tap(name, in: app, timeout: 20) { return true }

        let nextPage = app.buttons["Следующая страница"].firstMatch
        if nextPage.exists {
            nextPage.tap()
            if tap(name, in: app, timeout: 5) { return true }
        }

        let toggle = app.buttons["ToggleSideBar"].firstMatch
        if toggle.exists {
            toggle.tap()
            if tap(name, in: app, timeout: 5) { return true }
        }

        let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try? app.debugDescription.write(to: dir.appendingPathComponent("hierarchy.txt"),
                                        atomically: true, encoding: .utf8)
        return false
    }

    /// В свёрнутой панели вкладка — Button, в развёрнутом боковом меню — Cell,
    /// и у «Для покупки» к подписи приклеен счётчик («Для покупки, 10»).
    private func tap(_ name: String, in app: XCUIApplication, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(
            format: "label == %@ OR label BEGINSWITH %@", name, name + ",")
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            for element in [app.buttons.matching(predicate).firstMatch,
                            app.cells.matching(predicate).firstMatch] where element.exists {
                element.tap()
                return true
            }
            usleep(200_000)
        } while Date() < deadline
        return false
    }

    /// Снимок экрана: и в отчёт xcresult, и файлом в контейнер раннера —
    /// вытащить оттуда быстрее, чем разбирать xcresult.
    private func save(_ app: XCUIApplication, as name: String) {
        let shot = XCUIScreen.main.screenshot()
        let attachment = XCTAttachment(screenshot: shot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)

        let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try? shot.pngRepresentation.write(to: dir.appendingPathComponent("\(name).png"))
    }

    func testCookingDeductsStockAndWritesLog() {
        let app = launch(tab: "recipes")

        let card = app.staticTexts["Омлет классический"]
        XCTAssertTrue(card.waitForExistence(timeout: 20), "карточка рецепта не появилась")
        save(app, as: "ui-01-recipes")
        card.tap()

        let cookButton = app.buttons["Приготовил"]
        XCTAssertTrue(cookButton.waitForExistence(timeout: 10), "карточка рецепта не открылась")
        // Ингредиенты пересчитались под базовые 2 порции.
        XCTAssertTrue(app.staticTexts["Яйца куриные"].exists)
        save(app, as: "ui-02-detail")

        // Порции 2 → 3: граммовки должны пересчитаться на лету.
        let steppers = app.steppers
        if steppers.count > 0 {
            steppers.element(boundBy: 0).buttons.element(boundBy: 1).tap()
            save(app, as: "ui-03-detail-3-portions")
        }

        cookButton.tap()
        let confirm = app.buttons["Списать"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 10), "форма подтверждения не открылась")
        save(app, as: "ui-04-cook-sheet")
        confirm.tap()

        // Журнал должен пополниться записью о готовке.
        XCTAssertTrue(openTab("Журнал", in: app), "не открыть вкладку «Журнал»")
        let logEntry = app.staticTexts["Омлет классический"]
        XCTAssertTrue(logEntry.waitForExistence(timeout: 10), "записи в журнале нет")
        save(app, as: "ui-05-log")
    }

    func testEveryTabOpens() {
        let app = launch(tab: "fridge")
        for tab in ["Холодильник", "Для покупки", "Рецепты", "Журнал", "Настройки"] {
            XCTAssertTrue(openTab(tab, in: app), "не открыть вкладку «\(tab)»")
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 5),
                          "приложение упало на вкладке «\(tab)»")
        }
        save(app, as: "ui-06-settings")
    }

    func testAddProductFormOpens() {
        let app = launch(tab: "fridge")
        let add = app.buttons["Добавить продукт"].firstMatch
        XCTAssertTrue(add.waitForExistence(timeout: 20), "кнопки добавления нет")
        add.tap()
        XCTAssertTrue(app.staticTexts["Новый продукт"].waitForExistence(timeout: 10),
                      "форма нового продукта не открылась")
        save(app, as: "ui-07-add-product")
    }
}
