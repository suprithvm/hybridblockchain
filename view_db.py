import plyvel
import json
import sys
from datetime import datetime

def view_leveldb(db_path):
    print(f"Opening database at: {db_path}")
    db = plyvel.DB(db_path, create_if_missing=False)
    
    print("\n=== Database Contents ===")
    for key, value in db:
        try:
            # Try to decode as string
            key_str = key.decode('utf-8')
            try:
                # Try to parse value as JSON
                value_str = json.loads(value.decode('utf-8'))
                print(f"\nKey: {key_str}")
                print("Value (JSON):")
                print(json.dumps(value_str, indent=2))
            except:
                # If not JSON, show raw value
                value_str = value.decode('utf-8', errors='ignore')
                print(f"\nKey: {key_str}")
                print(f"Value: {value_str}")
        except:
            # If binary, show hex
            print(f"\nKey (hex): {key.hex()}")
            print(f"Value (hex): {value.hex()}")
    
    db.close()

if __name__ == "__main__":
    db_path = "./validator_data/nodedata"
    if len(sys.argv) > 1:
        db_path = sys.argv[1]
    
    try:
        view_leveldb(db_path)
    except Exception as e:
        print(f"Error: {e}")
        print("\nUsage: python view_db.py [path_to_db]")
        print("Default path: ./validator_data/nodedata") 