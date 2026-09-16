import EventKit
import Foundation

/// Экспорт списка покупок в системные Напоминания: настоящий список «Купить»
/// с чекбоксами, а не текстовая копия.
@MainActor
final class RemindersService {
    static let shared = RemindersService()

    static let listName = "Купить"

    enum ExportError: LocalizedError {
        case accessDenied
        case noSource
        case underlying(String)

        var errorDescription: String? {
            switch self {
            case .accessDenied:
                "Нет доступа к Напоминаниям. Разрешите его в Настройках → Sasha’s Kitchen."
            case .noSource:
                "Не нашёлся ни один аккаунт Напоминаний, куда можно записать список."
            case .underlying(let message):
                message
            }
        }
    }

    struct ExportResult {
        var added: Int
        var alreadyThere: Int
    }

    private let store = EKEventStore()

    var authorizationStatus: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .reminder)
    }

    @discardableResult
    func requestAccess() async -> Bool {
        (try? await store.requestFullAccessToReminders()) ?? false
    }

    /// Добавляет в список только те позиции, которых там ещё нет: повторный
    /// экспорт не должен плодить дубликаты.
    func export(titles: [String]) async throws -> ExportResult {
        guard await requestAccess() else { throw ExportError.accessDenied }

        let calendar = try reminderList()
        let existing = await existingTitles(in: calendar)

        var added = 0
        var alreadyThere = 0
        for title in titles {
            guard !existing.contains(title) else { alreadyThere += 1; continue }
            let reminder = EKReminder(eventStore: store)
            reminder.title = title
            reminder.calendar = calendar
            do {
                try store.save(reminder, commit: false)
                added += 1
            } catch {
                throw ExportError.underlying(error.localizedDescription)
            }
        }

        do {
            try store.commit()
        } catch {
            throw ExportError.underlying(error.localizedDescription)
        }
        return ExportResult(added: added, alreadyThere: alreadyThere)
    }

    private func reminderList() throws -> EKCalendar {
        if let existing = store.calendars(for: .reminder).first(where: { $0.title == Self.listName }) {
            return existing
        }
        guard let source = writableSource() else { throw ExportError.noSource }
        let calendar = EKCalendar(for: .reminder, eventStore: store)
        calendar.title = Self.listName
        calendar.source = source
        do {
            try store.saveCalendar(calendar, commit: true)
        } catch {
            throw ExportError.underlying(error.localizedDescription)
        }
        return calendar
    }

    private func writableSource() -> EKSource? {
        store.defaultCalendarForNewReminders()?.source
            ?? store.sources.first { $0.sourceType == .calDAV || $0.sourceType == .local }
            ?? store.sources.first
    }

    private func existingTitles(in calendar: EKCalendar) async -> Set<String> {
        let predicate = store.predicateForIncompleteReminders(
            withDueDateStarting: nil, ending: nil, calendars: [calendar])
        return await withCheckedContinuation { continuation in
            store.fetchReminders(matching: predicate) { reminders in
                continuation.resume(returning: Set((reminders ?? []).compactMap(\.title)))
            }
        }
    }
}
