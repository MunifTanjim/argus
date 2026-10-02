package dev.muniftanjim.argus

import android.content.Intent
import android.os.Build
import android.speech.RecognitionSupport
import android.speech.RecognitionSupportCallback
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "dev.muniftanjim.argus/speech")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "installedLanguages" -> installedLanguages(result)
                    else -> result.notImplemented()
                }
            }
    }

    // The stts plugin offers no way to read which on-device speech packs are
    // installed. Null when the device cannot download packs: below Android 14,
    // or no on-device recognizer.
    private fun installedLanguages(result: MethodChannel.Result) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE ||
            !SpeechRecognizer.isOnDeviceRecognitionAvailable(this)
        ) {
            result.success(null)
            return
        }
        val recognizer = SpeechRecognizer.createOnDeviceSpeechRecognizer(this)
        recognizer.checkRecognitionSupport(
            Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH),
            mainExecutor,
            object : RecognitionSupportCallback {
                override fun onSupportResult(support: RecognitionSupport) {
                    result.success(support.installedOnDeviceLanguages)
                    recognizer.destroy()
                }

                override fun onError(error: Int) {
                    result.success(null)
                    recognizer.destroy()
                }
            },
        )
    }
}
