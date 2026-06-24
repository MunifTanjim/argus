# MLKit barcode scanning (mobile_scanner). R8 strips/renames MLKit's internal
# subpackage classes, which MLKit instantiates by name via its component
# registrar — when renamed, the lookup returns null and BarcodeScanning.getClient()
# NPEs (e.g. com.google.mlkit.vision.barcode.internal.zzg). mobile_scanner's
# bundled consumer rule only keeps `com.google.mlkit.*` (single star, no
# subpackages), so close the gap here with `**`.
-keep class com.google.mlkit.** { *; }
-keep class com.google.android.gms.internal.mlkit_vision_barcode.** { *; }
-keep class com.google.android.gms.internal.mlkit_vision_common.** { *; }
-keep class com.google.android.libraries.barhopper.** { *; }
-dontwarn com.google.mlkit.**
