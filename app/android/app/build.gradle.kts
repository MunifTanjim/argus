import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Release signing config, read from android/key.properties (gitignored). Absent
// on machines that only do debug builds, in which case the release build falls
// back to debug keys below.
val keystoreProperties = Properties().apply {
    val f = rootProject.file("key.properties")
    if (f.exists()) f.inputStream().use { load(it) }
}

android {
    namespace = "dev.muniftanjim.argus"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        // Required by flutter_local_notifications (uses java.time on older APIs).
        isCoreLibraryDesugaringEnabled = true
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "dev.muniftanjim.argus"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        create("release") {
            val storePath = keystoreProperties["storeFile"] as String?
            if (storePath != null) {
                keyAlias = keystoreProperties["keyAlias"] as String
                keyPassword = keystoreProperties["keyPassword"] as String
                storeFile = file(storePath)
                storePassword = keystoreProperties["storePassword"] as String
            }
        }
    }

    buildTypes {
        release {
            // Use the upload key when key.properties is present (Play uploads),
            // else fall back to debug keys so `flutter run --release` still works.
            signingConfig = if (keystoreProperties.isEmpty)
                signingConfigs.getByName("debug")
            else
                signingConfigs.getByName("release")
            // R8 runs in release; apply our keep rules (proguard-rules.pro) so MLKit's
            // internal barcode classes aren't renamed (else BarcodeScanning.getClient
            // returns null → NPE in mobile_scanner).
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

// flutter_secure_storage pulls com.google.crypto.tink:tink-android; another
// dependency pulls the plain JVM com.google.crypto.tink:tink, whose classes
// collide (duplicate-class build failure). Keep the Android variant, drop the
// JVM one.
configurations.all {
    exclude(group = "com.google.crypto.tink", module = "tink")
}

dependencies {
    coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:2.1.4")
    // Embedded UnifiedPush distributor that registers via Web Push + VAPID; the
    // in-app fallback when no external distributor is installed.
    implementation("org.unifiedpush.android:embedded-fcm-distributor:3.0.0")
}
