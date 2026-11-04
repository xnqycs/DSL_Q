import AppKit
import Foundation
let directory = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
for size in [16,32,128,256,512] {
    for scale in [1,2] {
        let pixels = size * scale
        let image = NSImage(size: NSSize(width: pixels,height:pixels))
        image.lockFocus()
        let p = CGFloat(pixels), inset = p * 0.07
        let rect = NSRect(x: inset,y:inset,width:p-inset*2,height:p-inset*2)
        let tile = NSBezierPath(roundedRect: rect,xRadius:p*0.2,yRadius:p*0.2)
        NSGradient(starting:NSColor(red:0.20,green:0.59,blue:1,alpha:1),ending:NSColor(red:0.08,green:0.25,blue:0.83,alpha:1))!.draw(in:tile,angle:-90)
        NSColor.white.setStroke()
        let arrow=NSBezierPath();arrow.lineWidth=p*0.065;arrow.lineCapStyle = .round;arrow.lineJoinStyle = .round
        arrow.move(to:NSPoint(x:p*0.5,y:p*0.74));arrow.line(to:NSPoint(x:p*0.5,y:p*0.36))
        arrow.move(to:NSPoint(x:p*0.35,y:p*0.50));arrow.line(to:NSPoint(x:p*0.5,y:p*0.35));arrow.line(to:NSPoint(x:p*0.65,y:p*0.50));arrow.stroke()
        let tray=NSBezierPath();tray.lineWidth=p*0.05;tray.lineCapStyle = .round;tray.lineJoinStyle = .round
        tray.move(to:NSPoint(x:p*0.29,y:p*0.32));tray.line(to:NSPoint(x:p*0.29,y:p*0.25));tray.line(to:NSPoint(x:p*0.71,y:p*0.25));tray.line(to:NSPoint(x:p*0.71,y:p*0.32));tray.stroke()
        image.unlockFocus()
        let bitmap=NSBitmapImageRep(data:image.tiffRepresentation!)!
        let suffix=scale==2 ? "@2x" : ""
        try bitmap.representation(using:.png,properties:[:])!.write(to:directory.appendingPathComponent("icon_\(size)x\(size)\(suffix).png"))
    }
}
