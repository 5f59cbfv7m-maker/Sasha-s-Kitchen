import AVFoundation
import Foundation

/// Сканер штрихкодов — фаза 2, но разрешение на камеру запрашивается уже
/// сейчас: к моменту, когда сканер появится, доступ у приложения уже будет.
@MainActor
enum CameraPermission {

    static var status: AVAuthorizationStatus {
        AVCaptureDevice.authorizationStatus(for: .video)
    }

    static var statusText: String {
        switch status {
        case .authorized: "Доступ разрешён"
        case .denied: "Доступ запрещён"
        case .restricted: "Доступ ограничен системой"
        case .notDetermined: "Разрешение ещё не запрашивалось"
        @unknown default: "Неизвестно"
        }
    }

    @discardableResult
    static func request() async -> Bool {
        await AVCaptureDevice.requestAccess(for: .video)
    }
}
