import CloudKit
import SwiftData
import SwiftUI

@main
struct FridgeOracleApp: App {
    @UIApplicationDelegateAdaptor private var appDelegate: AppDelegate
    let container: ModelContainer

    init() {
        // `-resetData YES` в аргументах запуска стирает базу и раскладывает
        // стартовые данные заново — UI-тестам нужен одинаковый холодильник
        // на каждом прогоне, а база переживает переустановку приложения.
        let isTestRun = UserDefaults.standard.bool(forKey: "resetData")
        container = KitchenStore.shared
        if isTestRun {
            SeedLoader.reset(container.mainContext)
            if StoreDemo.isOn { StoreDemo.apply(to: container.mainContext) }
        } else {
            SharedLists.shared.start(context: container.mainContext)
        }
        Feedback.shared.warmUp()
    }

    var body: some Scene {
        WindowGroup {
            KitchenRootView()
        }
    }
}

/// Какая кухня открыта: своя или семейная, в которую пригласили. Чужая
/// кухня — своя база, и все экраны просто работают с ней.
struct KitchenRootView: View {
    @AppStorage(FamilyKitchen.activeKey) private var active = ""

    var body: some View {
        let kitchen = FamilyKitchen.shared
        let family = kitchen.joined[active]
        RootView(familyAccess: family?.access)
            .modelContainer(family == nil ? KitchenStore.shared : kitchen.container(for: active))
            .id(family == nil ? "" : active)
            // Любое сохранение — повод отправить изменения семейной кухни.
            .onReceive(NotificationCenter.default.publisher(for: ModelContext.didSave)) { _ in kitchen.reconcile() }
    }
}

/// Приглашение в общий список приходит сцене: открыли ссылку из «Сообщений»
/// при запущенном приложении — `userDidAcceptCloudKitShareWith`, при холодном
/// старте — в параметрах подключения сцены.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, configurationForConnecting session: UISceneSession,
                     options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        let configuration = UISceneConfiguration(name: nil, sessionRole: session.role)
        configuration.delegateClass = SceneDelegate.self
        return configuration
    }
}

final class SceneDelegate: NSObject, UIWindowSceneDelegate {
    func scene(_ scene: UIScene, willConnectTo session: UISceneSession,
               options connectionOptions: UIScene.ConnectionOptions) {
        if let metadata = connectionOptions.cloudKitShareMetadata { SharedLists.accept(metadata) }
        // `-windowSize 1440x900` — окно ровно под скриншот App Store для Mac.
        if let size = UserDefaults.standard.string(forKey: "windowSize")?.split(separator: "x").compactMap({ Double($0) }),
           size.count == 2, let windowScene = scene as? UIWindowScene {
            let frame = CGSize(width: size[0], height: size[1])
            windowScene.sizeRestrictions?.minimumSize = frame
            windowScene.sizeRestrictions?.maximumSize = frame
        }
    }

    func windowScene(_ windowScene: UIWindowScene,
                     userDidAcceptCloudKitShareWith cloudKitShareMetadata: CKShare.Metadata) {
        SharedLists.accept(cloudKitShareMetadata)
    }
}
