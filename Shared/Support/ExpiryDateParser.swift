import Foundation

/// Дата «годен до» из текста на упаковке: 05.11.2026, 05/11/26, 2026-11-05,
/// 2026年11月5日, 11/2026 (до конца месяца). Порядок день/месяц — как в регионе:
/// в США месяц первым, если иначе дата не складывается.
nonisolated enum ExpiryDateParser {

    static func parse(_ text: String, monthFirst: Bool = Locale.current.region == .unitedStates,
                      now: Date = .now, calendar: Calendar = Calendar(identifier: .gregorian)) -> Date? {
        let candidates = dates(in: text, monthFirst: monthFirst, calendar: calendar)
        // Отбрасываем то, что не похоже на срок годности: дату изготовления
        // давностью больше года и опечатки распознавания из далёкого будущего.
        let earliest = calendar.date(byAdding: .year, value: -1, to: now) ?? now
        let latest = calendar.date(byAdding: .year, value: 10, to: now) ?? now
        return candidates.filter { $0 >= earliest && $0 <= latest }.max()
    }

    private static func dates(in text: String, monthFirst: Bool, calendar: Calendar) -> [Date] {
        func make(_ year: Int, _ month: Int, _ day: Int?) -> Date? {
            let fullYear = year < 100 ? 2000 + year : year
            guard (1...12).contains(month) else { return nil }
            if let day {
                guard (1...31).contains(day) else { return nil }
                let date = calendar.date(from: DateComponents(year: fullYear, month: month, day: day))
                // 31.02 календарь перенёс бы на март — такого числа нет.
                return date.flatMap { calendar.component(.month, from: $0) == month ? $0 : nil }
            }
            // Только месяц и год — годен до конца месяца.
            guard let first = calendar.date(from: DateComponents(year: fullYear, month: month, day: 1)),
                  let next = calendar.date(byAdding: .month, value: 1, to: first) else { return nil }
            return calendar.date(byAdding: .day, value: -1, to: next)
        }
        var result: [Date] = []
        for match in text.matches(of: /(\d{4})\s*[.\/\-年]\s*(\d{1,2})\s*[.\/\-月]\s*(\d{1,2})/) {
            if let date = make(Int(match.output.1)!, Int(match.output.2)!, Int(match.output.3)!) { result.append(date) }
        }
        for match in text.matches(of: /(?:^|[^\d])(\d{1,2})\s*[.\/\-]\s*(\d{1,2})\s*[.\/\-]\s*(\d{4}|\d{2})(?!\d)/) {
            let a = Int(match.output.1)!, b = Int(match.output.2)!, year = Int(match.output.3)!
            let (month, day) = monthFirst && a <= 12 ? (a, b) : (b, a)
            if let date = make(year, month, day) ?? make(year, day, month) { result.append(date) }
        }
        if result.isEmpty {
            for match in text.matches(of: /(?:^|[^\d.\/])(\d{1,2})\s*[.\/]\s*(\d{4})(?![\d.\/])/) {
                if let date = make(Int(match.output.2)!, Int(match.output.1)!, nil) { result.append(date) }
            }
        }
        return result
    }
}
