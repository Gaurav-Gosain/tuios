# Prints kitty's own encoding of a key matrix, the ground truth for
# TestEncoderMatchesKitty. Run it with kitty's interpreter:
#
#   kitty +runpy "exec(open('generate.py').read())" > golden.txt
#
# Each line: FLAGS KEY ACTION REPR, where REPR is a Python string literal.
import kitty.fast_data_types as f
e=f.encode_key_for_tty
keys={'enter':(f.GLFW_FKEY_ENTER,''),'tab':(f.GLFW_FKEY_TAB,''),'bs':(f.GLFW_FKEY_BACKSPACE,''),'a':(ord('a'),'a'),
 'esc':(f.GLFW_FKEY_ESCAPE,''),'space':(32,' '),'up':(f.GLFW_FKEY_UP,''),'f5':(f.GLFW_FKEY_F5,''),'del':(f.GLFW_FKEY_DELETE,''),
 '1':(ord('1'),'1'),'kp1':(f.GLFW_FKEY_KP_1,'1'),'lshift':(f.GLFW_FKEY_LEFT_SHIFT,'')}
mods=[(0,''),(f.GLFW_MOD_SHIFT,'shift+'),(f.GLFW_MOD_CONTROL,'ctrl+'),(f.GLFW_MOD_ALT,'alt+'),(f.GLFW_MOD_NUM_LOCK,'num+')]
acts={'press':f.GLFW_PRESS,'release':f.GLFW_RELEASE,'repeat':f.GLFW_REPEAT}
shifted={'a':'A','1':'!','space':' '}
for flags in range(1,32):
  for k,(code,text) in keys.items():
    for m,mn in mods:
      for an,a in acts.items():
        t=text
        sk=0
        if m==f.GLFW_MOD_SHIFT and k in shifted:
            t=shifted[k]; sk=ord(shifted[k]) if k!='space' else 0
        if m in (f.GLFW_MOD_CONTROL,f.GLFW_MOD_ALT): t=''
        if a==f.GLFW_RELEASE: t=''
        r=e(key=code, shifted_key=sk, alternate_key=0, mods=m, action=a, text=t, key_encoding_flags=flags, cursor_key_mode=False)
        print(flags, mn+k, an, repr(r))
