import AVFoundation
import EventKit
import SwiftData
import SwiftUI

struct SettingsView: View {
    @Environment(\.modelContext) private var context
    @Query private var products: [Product]
    @Query private var recipes: [Recipe]
    @Query private var logs: [CookingLog]

    @AppStorage(Feedback.soundKey) private var soundEnabled = true
    @AppStorage(Feedback.hapticsKey) private var hapticsEnabled = true
    @AppStorage(ExpiryReminders.enabledKey) private var expiryEnabled = false
    @AppStorage(ExpiryReminders.timeKey) private var expiryMinutes = 600
    @AppStorage(UnitSystem.key) private var unitSystem = UnitSystem.automatic.rawValue
    @AppStorage(SeedSet.key) private var starterSet = SeedSet.current.rawValue
    @State private var notificationsDenied = false

    @State private var cameraStatus = CameraPermission.statusText
    @State private var remindersStatus = ""
    @State private var isResetting = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Toggle("Звуковые эффекты", isOn: $soundEnabled)
                    Toggle("Тактильный отклик", isOn: $hapticsEnabled)
                    Button {
                        Feedback.shared.play(.cooked)
                    } label: {
                        Label("Проверить звук", systemImage: "speaker.wave.2")
                    }
                } header: {
                    Text("Отклик")
                } footer: {
                    Text("Вибро работает на iPhone; на iPad и Mac основной отклик — звук и анимация.")
                }

                Section {
                    Picker("Единицы измерения", selection: $unitSystem) {
                        Text("Как в системе").tag(UnitSystem.automatic.rawValue)
                        Text("Метрические — г, мл").tag(UnitSystem.metric.rawValue)
                        Text("Американские — oz, cups").tag(UnitSystem.us.rawValue)
                    }
                } footer: {
                    Text("Продукты хранятся в граммах и миллилитрах; от выбора зависит только то, как количества показываются и вводятся. КБЖУ — всегда на 100 г.")
                }

                Section {
                    Toggle("Напоминать о сроках годности", isOn: expiryToggle)
                    if expiryEnabled {
                        DatePicker("Время уведомления", selection: expiryTime, displayedComponents: .hourAndMinute)
                    }
                } header: {
                    Text("Сроки годности")
                } footer: {
                    Text(notificationsDenied
                         ? "Уведомления для Sasha’s Kitchen запрещены в настройках устройства — разрешите их там."
                         : "Накануне дня, когда истекает срок, придёт уведомление: что испортится и что из этого можно приготовить. Включается на каждом устройстве отдельно.")
                }

                Section {
                    LabeledContent("Камера", value: cameraStatus)
                    Button {
                        Task {
                            await CameraPermission.request()
                            cameraStatus = CameraPermission.statusText
                        }
                    } label: {
                        Label("Запросить доступ к камере", systemImage: "camera")
                    }
                    .disabled(CameraPermission.status != .notDetermined)
                } header: {
                    Text("Сканер штрихкода")
                } footer: {
                    Text("Сканер — на вкладках «Холодильник» и «Для покупки». Название и КБЖУ приходят из открытой базы Open Food Facts; туда уходит только сам штрихкод.")
                }

                Section {
                    LabeledContent("Напоминания", value: remindersStatus)
                    Button {
                        Task {
                            await RemindersService.shared.requestAccess()
                            remindersStatus = Self.remindersText()
                        }
                    } label: {
                        Label("Запросить доступ к Напоминаниям", systemImage: "checklist")
                    }
                    .disabled(RemindersService.shared.authorizationStatus != .notDetermined)
                } header: {
                    Text("Список покупок")
                } footer: {
                    Text("Кнопка «В Напоминания» на вкладке «Для покупки» создаёт список «\(RemindersService.listName)» с настоящими чекбоксами.")
                }

                Section {
                    LabeledContent("Продуктов в каталоге", value: "\(products.count)")
                    LabeledContent("Рецептов", value: "\(recipes.count)")
                    LabeledContent("Записей в журнале", value: "\(logs.count)")
                    Picker("Стартовый набор", selection: $starterSet) {
                        ForEach(SeedSet.allCases, id: \.self) { Text($0.title).tag($0.rawValue) }
                    }
                    Button(role: .destructive) {
                        isResetting = true
                    } label: {
                        Label("Загрузить стартовые данные заново", systemImage: "arrow.clockwise")
                    }
                } header: {
                    Text("Данные")
                } footer: {
                    Text("Сброс удалит все остатки, свои рецепты, список покупок и журнал — на всех ваших устройствах, — а затем разложит заново стартовый набор продуктов и рецептов.")
                }

                Section {
                    LabeledContent("iCloud", value: Self.iCloudText)
                } header: {
                    Text("Синхронизация")
                } footer: {
                    Text("Холодильник, рецепты, список покупок и журнал хранятся в вашем iCloud и сами появляются на iPhone, iPad, Mac и Apple Watch с той же учётной записью Apple. Список покупок можно открыть и другим людям: «Поделиться» → «Пригласить в общий список».")
                }

                Section {
                    LabeledContent("Версия", value: Self.version)
                    #if !targetEnvironment(macCatalyst)
                    if let url = URL(string: UIApplication.openSettingsURLString) {
                        Link(destination: url) {
                            Label("Язык приложения", systemImage: "globe")
                        }
                    }
                    #endif
                } header: {
                    Text("О приложении")
                } footer: {
                    Text("Язык интерфейса меняется в системных настройках приложения. Названия продуктов и рецептов можно вводить на любом языке.")
                }
            }
            .navigationTitle("Настройки")
            .alert("Сбросить данные?", isPresented: $isResetting) {
                Button("Отмена", role: .cancel) {}
                Button("Сбросить", role: .destructive) {
                    SeedLoader.reset(context)
                    Feedback.shared.play(.cooked)
                }
            } message: {
                Text("Свои продукты, рецепты и журнал будут удалены без возможности вернуть.")
            }
            .onAppear {
                cameraStatus = CameraPermission.statusText
                remindersStatus = Self.remindersText()
            }
        }
    }

    private var expiryToggle: Binding<Bool> {
        Binding {
            expiryEnabled
        } set: { isOn in
            Task {
                if isOn {
                    notificationsDenied = !(await ExpiryReminders.enable())
                } else {
                    expiryEnabled = false
                }
                await ExpiryReminders.reschedule(in: context)
            }
        }
    }

    private var expiryTime: Binding<Date> {
        Binding {
            Calendar.current.date(byAdding: .minute, value: expiryMinutes, to: Calendar.current.startOfDay(for: .now)) ?? .now
        } set: { date in
            let parts = Calendar.current.dateComponents([.hour, .minute], from: date)
            expiryMinutes = (parts.hour ?? 10) * 60 + (parts.minute ?? 0)
            Task { await ExpiryReminders.reschedule(in: context) }
        }
    }

    private static func remindersText() -> String {
        switch EKEventStore.authorizationStatus(for: .reminder) {
        case .fullAccess: String(localized: "Доступ разрешён")
        case .writeOnly: String(localized: "Только запись")
        case .denied: String(localized: "Доступ запрещён")
        case .restricted: String(localized: "Доступ ограничен системой")
        case .notDetermined: String(localized: "Разрешение ещё не запрашивалось")
        @unknown default: String(localized: "Неизвестно")
        }
    }

    /// Только вход в iCloud: состояние самой синхронизации SwiftData наружу не отдаёт.
    private static var iCloudText: String {
        FileManager.default.ubiquityIdentityToken != nil
            ? String(localized: "Включена")
            : String(localized: "Войдите в iCloud в настройках устройства")
    }

    private static var version: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "1.0"
        let build = info?["CFBundleVersion"] as? String ?? "1"
        return "\(short) (\(build))"
    }
}

extension SeedSet {
    var title: String {
        switch self {
        case .russia: String(localized: "Россия и СНГ")
        case .west: String(localized: "Европа и Америка")
        case .eastAsia: String(localized: "Япония, Корея, Китай")
        case .thailand: String(localized: "Таиланд")
        }
    }
}
