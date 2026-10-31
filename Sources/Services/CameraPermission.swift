import AVFoundation
import Foundation

/// Разрешение на камеру для сканера штрихкодов (`ScanSheet`). Сам сканер
/// спросит его при первом запуске; здесь — статус и запрос из настроек.
@MainActor
enum CameraPermission {

    static var status: AVAuthorizationStatus {
        AVCaptureDevice.authorizationStatus(for: .video)
    }

    static var statusText: String {
        switch status {
        case .authorized: String(localized: "Доступ разрешён")
        case .denied: String(localized: "Доступ запрещён")
        case .restricted: String(localized: "Доступ ограничен системой")
        case .notDetermined: String(localized: "Разрешение ещё не запрашивалось")
        @unknown default: String(localized: "Неизвестно")
        }
    }

    @discardableResult
    static func request() async -> Bool {
        await AVCaptureDevice.requestAccess(for: .video)
    }
}
