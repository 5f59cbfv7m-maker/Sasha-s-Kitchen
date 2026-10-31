import Foundation
import Testing
@testable import FridgeOracle

@Suite("Дата «годен до» с упаковки")
struct ExpiryDateParserTests {
    private let calendar = Calendar(identifier: .gregorian)
    private var now: Date { calendar.date(from: DateComponents(year: 2026, month: 10, day: 31))! }

    private func ymd(_ date: Date?) -> String? {
        date.map { let c = calendar.dateComponents([.year, .month, .day], from: $0); return "\(c.year!)-\(c.month!)-\(c.day!)" }
    }

    @Test("Форматы со всего мира", arguments: [
        ("Годен до: 05.11.2026", "2026-11-5"),
        ("ГОДЕН ДО 05.11.26", "2026-11-5"),
        ("BEST BEFORE 2026-12-01", "2026-12-1"),
        ("賞味期限 2027.01.15", "2027-1-15"),
        ("保质期至2026年11月5日", "2026-11-5"),
        ("EXP 11/2026", "2026-11-30"),
        ("изг. 01.10.2026 годен до 10.11.2026", "2026-11-10"),
    ])
    func parses(_ text: String, _ expected: String) {
        #expect(ymd(ExpiryDateParser.parse(text, monthFirst: false, now: now, calendar: calendar)) == expected)
    }

    @Test("В США месяц первым; невозможная дата и давняя дата — нет")
    func ambiguity() {
        #expect(ymd(ExpiryDateParser.parse("11/05/2026", monthFirst: true, now: now, calendar: calendar)) == "2026-11-5")
        #expect(ymd(ExpiryDateParser.parse("11/05/2026", monthFirst: false, now: now, calendar: calendar)) == "2026-5-11")
        #expect(ExpiryDateParser.parse("31.02.2026", monthFirst: false, now: now, calendar: calendar) == nil)
        #expect(ExpiryDateParser.parse("01.01.2019", monthFirst: false, now: now, calendar: calendar) == nil)
        #expect(ExpiryDateParser.parse("Молоко 3,2%", monthFirst: false, now: now, calendar: calendar) == nil)
    }
}
